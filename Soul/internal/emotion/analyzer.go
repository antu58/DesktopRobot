package emotion

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	Schema = "compact"
	Engine = "go-lexical-v1"
)

type PAD struct {
	P float64 `json:"p"`
	A float64 `json:"a"`
	D float64 `json:"d"`
}

type Result struct {
	Emotion       string  `json:"emotion"`
	P             float64 `json:"p"`
	A             float64 `json:"a"`
	D             float64 `json:"d"`
	Intensity     float64 `json:"intensity"`
	CoarseEmotion string  `json:"coarse_emotion,omitempty"`
}

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

var coreEmotions = []string{
	"neutral", "joy", "surprise", "sadness", "fear", "anger", "disgust",
}

// Compact baseline + commonly used refined emotions from prior regression.
var padMap = map[string]PAD{
	"neutral":        {P: 0.00, A: 0.05, D: 0.00},
	"joy":            {P: 0.70, A: 0.55, D: 0.20},
	"surprise":       {P: 0.10, A: 0.75, D: -0.05},
	"sadness":        {P: -0.65, A: -0.15, D: -0.35},
	"fear":           {P: -0.70, A: 0.70, D: -0.60},
	"anger":          {P: -0.60, A: 0.75, D: 0.25},
	"disgust":        {P: -0.55, A: 0.35, D: 0.10},
	"calm":           {P: 0.20, A: -0.35, D: 0.15},
	"relief":         {P: 0.50, A: -0.20, D: 0.30},
	"gratitude":      {P: 0.60, A: 0.20, D: 0.35},
	"excitement":     {P: 0.78, A: 0.82, D: 0.30},
	"anxiety":        {P: -0.62, A: 0.72, D: -0.48},
	"frustration":    {P: -0.52, A: 0.58, D: -0.08},
	"disappointment": {P: -0.58, A: -0.08, D: -0.28},
	"boredom":        {P: -0.20, A: -0.45, D: -0.15},
	"hope":           {P: 0.45, A: 0.35, D: 0.25},
	"pride":          {P: 0.65, A: 0.45, D: 0.55},
	"guilt":          {P: -0.45, A: 0.15, D: -0.45},
	"embarrassment":  {P: -0.28, A: 0.48, D: -0.38},
	"confusion":      {P: -0.10, A: 0.30, D: -0.20},
	"resignation":    {P: -0.30, A: -0.20, D: -0.40},
}

func PADTable() map[string]PAD {
	out := make(map[string]PAD, len(padMap))
	for k, v := range padMap {
		out[k] = v
	}
	return out
}

func Labels() []string {
	labels := make([]string, 0, len(padMap))
	for k := range padMap {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	return labels
}

var labelAliases = map[string]string{
	"neutral":        "neutral",
	"calm":           "calm",
	"boredom":        "boredom",
	"joy":            "joy",
	"excitement":     "excitement",
	"relief":         "relief",
	"gratitude":      "gratitude",
	"happy":          "joy",
	"happiness":      "joy",
	"love":           "joy",
	"surprise":       "surprise",
	"surprised":      "surprise",
	"sadness":        "sadness",
	"sad":            "sadness",
	"fear":           "fear",
	"anxiety":        "anxiety",
	"anxious":        "anxiety",
	"scared":         "fear",
	"anger":          "anger",
	"angry":          "anger",
	"frustration":    "frustration",
	"frustrated":     "frustration",
	"disappointment": "disappointment",
	"disappointed":   "disappointment",
	"disgust":        "disgust",
	"hope":           "hope",
	"hopeful":        "hope",
	"pride":          "pride",
	"proud":          "pride",
	"guilt":          "guilt",
	"guilty":         "guilt",
	"embarrassment":  "embarrassment",
	"embarrassed":    "embarrassment",
	"confusion":      "confusion",
	"confused":       "confusion",
	"resignation":    "resignation",
	"resigned":       "resignation",
	"平静":             "calm",
	"无聊":             "boredom",
	"开心":             "joy",
	"高兴":             "joy",
	"兴奋":             "excitement",
	"释然":             "relief",
	"感激":             "gratitude",
	"惊讶":             "surprise",
	"难过":             "sadness",
	"沮丧":             "disappointment",
	"失望":             "disappointment",
	"害怕":             "fear",
	"恐惧":             "fear",
	"焦虑":             "anxiety",
	"生气":             "anger",
	"愤怒":             "anger",
	"挫败":             "frustration",
	"烦躁":             "frustration",
	"厌恶":             "disgust",
	"希望":             "hope",
	"盼望":             "hope",
	"自豪":             "pride",
	"骄傲":             "pride",
	"内疚":             "guilt",
	"愧疚":             "guilt",
	"尴尬":             "embarrassment",
	"社死":             "embarrassment",
	"困惑":             "confusion",
	"懵":              "confusion",
	"算了":             "resignation",
	"认了":             "resignation",
}

var emotionHints = []struct {
	emotion string
	hints   []string
}{
	{emotion: "disgust", hints: []string{"反胃", "恶心", "卫生太差", "太脏", "臭烘烘", "disgusting", "gross"}},
	{emotion: "surprise", hints: []string{"惊讶", "没想到", "居然", "竟然", "surprised", "unexpected"}},
	{emotion: "calm", hints: []string{"平静", "平平淡淡", "还行", "一般", "没事", "普通", "没有特别开心", "也不难过", "calm"}},
	{emotion: "boredom", hints: []string{"无聊", "提不起劲", "没意思", "发呆", "boring"}},
	{emotion: "joy", hints: []string{"开心", "高兴", "轻快", "太好了", "顺利", "joyful"}},
	{emotion: "gratitude", hints: []string{"感谢", "谢谢", "感激", "被认可", "appreciate", "grateful"}},
	{emotion: "relief", hints: []string{"松了一口气", "终于", "还清", "解脱", "松口气", "提前完成", "搞定", "relieved"}},
	{emotion: "excitement", hints: []string{"太棒了", "激动", "兴奋", "演唱会", "抢到", "前排票", "爽", "excited"}},
	{emotion: "anxiety", hints: []string{"慌", "紧张", "不敢", "手心全是汗", "担心", "焦虑", "anxious", "worried"}},
	{emotion: "disappointment", hints: []string{"失望", "发挥失常", "对不起自己", "卖完了", "被否定", "落空", "disappointed"}},
	{emotion: "frustration", hints: []string{"无语", "烦", "加班", "被批评", "批评", "点名", "堵", "鸽子", "找茬", "frustrated"}},
	{emotion: "hope", hints: []string{"希望", "盼着", "但愿", "应该能", "愿望", "还有机会", "hope"}},
	{emotion: "pride", hints: []string{"自豪", "骄傲", "争气", "拿奖", "表扬", "proud"}},
	{emotion: "guilt", hints: []string{"内疚", "愧疚", "自责", "对不起", "抱歉", "迟到", "耽误", "guilty"}},
	{emotion: "embarrassment", hints: []string{"尴尬", "社死", "丢脸", "不好意思", "embarrassed"}},
	{emotion: "confusion", hints: []string{"困惑", "迷糊", "搞不明白", "没搞懂", "懵", "看不懂", "confused"}},
	{emotion: "resignation", hints: []string{"算了", "认了", "就这样吧", "无所谓了", "resigned"}},
	{emotion: "anger", hints: []string{"混蛋", "滚", "闭嘴", "气死", "找茬", "angry"}},
}

func coarseOf(emotion string) string {
	switch emotion {
	case "calm", "boredom", "confusion":
		return "neutral"
	case "relief", "gratitude", "excitement", "hope", "pride":
		return "joy"
	case "anxiety":
		return "fear"
	case "frustration":
		return "anger"
	case "disappointment", "guilt", "embarrassment", "resignation":
		return "sadness"
	default:
		return emotion
	}
}

func normalizeLabel(label string) string {
	key := strings.TrimSpace(strings.ToLower(label))
	if key == "" {
		return ""
	}
	if _, ok := padMap[key]; ok {
		return key
	}
	aliased, ok := labelAliases[key]
	if !ok {
		return ""
	}
	if _, ok := padMap[aliased]; !ok {
		return ""
	}
	return aliased
}

func containsAny(text string, hints []string) bool {
	for _, h := range hints {
		if strings.Contains(text, strings.ToLower(h)) {
			return true
		}
	}
	return false
}

func fineScores(text string) map[string]float64 {
	scores := make(map[string]float64, len(padMap))
	for k := range padMap {
		scores[k] = 0
	}

	for _, item := range emotionHints {
		for _, h := range item.hints {
			if strings.Contains(text, strings.ToLower(h)) {
				weight := 1.0 + math.Min(float64(utf8.RuneCountInString(h))/10.0, 1.0)
				scores[item.emotion] += weight
			}
		}
	}

	if strings.Contains(text, "!") || strings.Contains(text, "！") {
		scores["excitement"] += 0.6
		scores["anger"] += 0.2
		scores["surprise"] += 0.2
	}
	if strings.Contains(text, "?") || strings.Contains(text, "？") {
		scores["confusion"] += 0.5
		scores["surprise"] += 0.2
		scores["anxiety"] += 0.2
	}

	return scores
}

func coarseScoresFromFine(scores map[string]float64) map[string]float64 {
	out := map[string]float64{
		"neutral":  0,
		"joy":      0,
		"surprise": 0,
		"sadness":  0,
		"fear":     0,
		"anger":    0,
		"disgust":  0,
	}
	for emo, s := range scores {
		out[coarseOf(emo)] += s
	}
	return out
}

func topLabel(scores map[string]float64, labels []string) string {
	top := labels[0]
	topScore := scores[top]
	for _, k := range labels[1:] {
		if scores[k] > topScore {
			top = k
			topScore = scores[k]
		}
	}
	return top
}

func totalScore(scores map[string]float64) float64 {
	total := 0.0
	for _, v := range scores {
		total += v
	}
	return total
}

func inferCoarseEmotion(scores map[string]float64) (string, float64) {
	coarseScores := coarseScoresFromFine(scores)
	total := totalScore(coarseScores)
	if total <= 1e-9 {
		coarseScores["neutral"] = 1.0
		total = 1.0
	}
	base := topLabel(coarseScores, coreEmotions)
	top := coarseScores[base]
	ratio := top / total
	evidence := math.Min(1.0, total/3.0)
	conf := clamp(0.52+0.33*ratio+0.15*evidence, 0.55, 0.995)
	if base == "neutral" && total <= 1.01 {
		conf = 0.58
	}
	return base, conf
}

func refineEmotion(text, baseEmotion string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return baseEmotion
	}

	// Strong lexical cues first.
	for _, item := range emotionHints {
		if containsAny(t, item.hints) {
			return item.emotion
		}
	}

	// Distribution-free fallback.
	if baseEmotion == "fear" {
		return "anxiety"
	}
	if baseEmotion == "sadness" {
		return "disappointment"
	}
	if baseEmotion == "anger" && (strings.Contains(t, "?") || strings.Contains(t, "？")) {
		return "frustration"
	}
	if baseEmotion == "joy" && (strings.Contains(t, "!") || strings.Contains(t, "！")) {
		return "excitement"
	}
	return baseEmotion
}

func (a *Analyzer) Analyze(text string) Result {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return a.Convert("neutral", 0.58)
	}
	scores := fineScores(t)
	baseEmotion, conf := inferCoarseEmotion(scores)
	refined := refineEmotion(t, baseEmotion)
	result := a.Convert(refined, conf)
	result.CoarseEmotion = baseEmotion
	return result
}

func (a *Analyzer) Convert(emotion string, confidence float64) Result {
	key := normalizeLabel(emotion)
	if key == "" {
		key = "neutral"
	}
	pad := padMap[key]
	return Result{
		Emotion:   key,
		P:         round(pad.P, 3),
		A:         round(pad.A, 3),
		D:         round(pad.D, 3),
		Intensity: round(clamp(confidence, 0.0, 1.0), 6),
	}
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func round(v float64, precision int) float64 {
	p := math.Pow10(precision)
	return math.Round(v*p) / p
}
