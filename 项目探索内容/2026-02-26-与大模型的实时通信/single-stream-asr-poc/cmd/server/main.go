package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"single-stream-asr-poc/internal/asr"

	"github.com/pion/webrtc/v4"
)

type offerRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type offerResponse struct {
	SDP       string `json:"sdp"`
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	ASRMode   string `json:"asr_mode"`
}

type server struct {
	asrMode     string
	bridgeURL   string
	api         *webrtc.API
	iceUDPPort  int
	icePublicIP string
	iceListener *net.UDPConn
}

func main() {
	addr := flag.String("addr", ":8088", "HTTP listen address")
	webDir := flag.String("web", "web", "frontend static directory")
	asrMode := flag.String("asr", getEnv("ASR_MODE", "auto"), "ASR mode: auto|bridge|mock")
	bridgeURL := flag.String("bridge-url", getEnv("ASR_BRIDGE_URL", "ws://127.0.0.1:2700/ws"), "ASR bridge websocket URL")
	iceUDPPort := flag.Int("ice-udp-port", getEnvInt("ICE_UDP_PORT", 19000), "UDP port for WebRTC ICE")
	icePublicIP := flag.String("ice-public-ip", getEnv("ICE_PUBLIC_IP", ""), "IP advertised in ICE host candidates (e.g. 127.0.0.1)")
	flag.Parse()

	api, iceListener, err := newWebRTCAPI(*iceUDPPort, *icePublicIP)
	if err != nil {
		log.Fatalf("init webrtc api failed: %v", err)
	}

	s := &server{
		asrMode:     *asrMode,
		bridgeURL:   *bridgeURL,
		api:         api,
		iceUDPPort:  *iceUDPPort,
		icePublicIP: *icePublicIP,
		iceListener: iceListener,
	}
	if err := s.assertReady(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/offer", s.handleOffer)

	absWebDir, err := filepath.Abs(*webDir)
	if err != nil {
		log.Fatalf("resolve web dir failed: %v", err)
	}
	mux.Handle("/", http.FileServer(http.Dir(absWebDir)))

	log.Printf("server starting on %s", *addr)
	log.Printf("web dir: %s", absWebDir)
	log.Printf("asr mode: %s, bridge: %s", s.asrMode, s.bridgeURL)
	log.Printf("ice udp port: %d, ice public ip: %s", s.iceUDPPort, s.icePublicIP)
	if err := http.ListenAndServe(*addr, withCORS(mux)); err != nil {
		log.Fatalf("listen failed: %v", err)
	}
}

func (s *server) handleOffer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req offerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("decode request failed: %v", err), http.StatusBadRequest)
		return
	}
	if req.SDP == "" || req.Type == "" {
		http.Error(w, "missing sdp/type", http.StatusBadRequest)
		return
	}

	sessionID := fmt.Sprintf("s-%d", time.Now().UnixNano())
	engine, mode, err := s.newEngine()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var (
		sendMu     sync.Mutex
		audioDC    *webrtc.DataChannel
		stream     asr.Stream
		streamOnce sync.Once
	)

	stream, err = engine.NewStream(sessionID, func(res asr.Result) {
		sendMu.Lock()
		defer sendMu.Unlock()
		if audioDC == nil || audioDC.ReadyState() != webrtc.DataChannelStateOpen {
			return
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"event":    "transcript",
			"text":     res.Text,
			"is_final": res.IsFinal,
			"source":   res.Source,
			"error":    res.Error,
		})
		if marshalErr != nil {
			return
		}
		if sendErr := audioDC.SendText(string(payload)); sendErr != nil {
			log.Printf("session=%s send transcript failed: %v", sessionID, sendErr)
		}
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("init asr stream failed: %v", err), http.StatusInternalServerError)
		return
	}

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		_ = stream.Close()
		http.Error(w, fmt.Sprintf("create peer connection failed: %v", err), http.StatusInternalServerError)
		return
	}

	cleanup := func() {
		streamOnce.Do(func() {
			if flushErr := stream.Flush(); flushErr != nil {
				log.Printf("session=%s flush failed: %v", sessionID, flushErr)
			}
			if closeErr := stream.Close(); closeErr != nil {
				log.Printf("session=%s stream close failed: %v", sessionID, closeErr)
			}
			_ = pc.Close()
		})
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("session=%s peer state=%s", sessionID, state.String())
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			cleanup()
		}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("session=%s ice state=%s", sessionID, state.String())
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("session=%s data channel open label=%s", sessionID, dc.Label())
		if dc.Label() != "audio" {
			return
		}

		sendMu.Lock()
		audioDC = dc
		sendMu.Unlock()

		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if msg.IsString {
				var evt struct {
					Event string `json:"event"`
				}
				if err := json.Unmarshal(msg.Data, &evt); err == nil && evt.Event == "flush" {
					_ = stream.Flush()
				}
				return
			}
			if len(msg.Data) == 0 {
				return
			}
			if err := stream.PushAudio(msg.Data); err != nil {
				log.Printf("session=%s push audio failed: %v", sessionID, err)
			}
		})

		dc.OnClose(func() {
			log.Printf("session=%s data channel closed", sessionID)
			cleanup()
		})
	})

	remoteOffer := webrtc.SessionDescription{
		Type: webrtc.NewSDPType(req.Type),
		SDP:  req.SDP,
	}
	if remoteOffer.Type == webrtc.SDPTypeUnknown {
		cleanup()
		http.Error(w, "invalid sdp type", http.StatusBadRequest)
		return
	}

	if err := pc.SetRemoteDescription(remoteOffer); err != nil {
		cleanup()
		http.Error(w, fmt.Sprintf("set remote description failed: %v", err), http.StatusBadRequest)
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		cleanup()
		http.Error(w, fmt.Sprintf("create answer failed: %v", err), http.StatusInternalServerError)
		return
	}

	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		cleanup()
		http.Error(w, fmt.Sprintf("set local description failed: %v", err), http.StatusInternalServerError)
		return
	}
	<-gatherDone

	local := pc.LocalDescription()
	if local == nil {
		cleanup()
		http.Error(w, "local description is empty", http.StatusInternalServerError)
		return
	}

	resp := offerResponse{
		SDP:       local.SDP,
		Type:      local.Type.String(),
		SessionID: sessionID,
		ASRMode:   mode,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *server) newEngine() (asr.Engine, string, error) {
	switch s.asrMode {
	case "mock":
		return &asr.MockEngine{}, "mock", nil
	case "bridge":
		return &asr.WSBridgeEngine{BaseURL: s.bridgeURL}, "bridge", nil
	case "auto":
		return &autoEngine{
			primary:   &asr.WSBridgeEngine{BaseURL: s.bridgeURL},
			secondary: &asr.MockEngine{},
		}, "auto", nil
	default:
		return nil, "", fmt.Errorf("unsupported ASR mode: %s", s.asrMode)
	}
}

type autoEngine struct {
	primary   asr.Engine
	secondary asr.Engine
}

func (a *autoEngine) Name() string {
	return "auto"
}

func (a *autoEngine) NewStream(sessionID string, onResult func(asr.Result)) (asr.Stream, error) {
	stream, err := a.primary.NewStream(sessionID, onResult)
	if err == nil {
		return stream, nil
	}
	if onResult != nil {
		onResult(asr.Result{
			Text:    fmt.Sprintf("桥接 ASR 不可用，切换为 mock: %v", err),
			IsFinal: false,
			Source:  "mock",
			Error:   err.Error(),
		})
	}
	if a.secondary == nil {
		return nil, err
	}
	return a.secondary.NewStream(sessionID, onResult)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getEnv(key, fallback string) string {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	raw, ok := os.LookupEnv(key)
	if !ok || raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func newWebRTCAPI(iceUDPPort int, icePublicIP string) (*webrtc.API, *net.UDPConn, error) {
	var se webrtc.SettingEngine
	var listener *net.UDPConn

	if iceUDPPort > 0 {
		udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{
			IP:   net.IPv4zero,
			Port: iceUDPPort,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("listen udp :%d failed: %w", iceUDPPort, err)
		}
		listener = udpConn
		udpMux := webrtc.NewICEUDPMux(nil, udpConn)
		se.SetICEUDPMux(udpMux)
	}

	if icePublicIP != "" {
		se.SetNAT1To1IPs([]string{icePublicIP}, webrtc.ICECandidateTypeHost)
	}

	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	return api, listener, nil
}

func (s *server) assertReady() error {
	if s.asrMode == "bridge" && s.bridgeURL == "" {
		return errors.New("bridge URL is required in bridge mode")
	}
	if s.api == nil {
		return errors.New("webrtc api is not initialized")
	}
	return nil
}
