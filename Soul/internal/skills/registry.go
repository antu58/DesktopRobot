package skills

import (
	"strings"
	"sync"
	"time"

	"soul/internal/domain"
)

type TerminalSkillState struct {
	TerminalID     string
	SoulID         string
	SkillVersion   int64
	Skills         []domain.SkillDefinition
	CatalogVersion int64
	IntentCatalog  []domain.IntentSpec
	Online         bool
	LastUpdated    time.Time
}

type Registry struct {
	mu       sync.RWMutex
	data     map[string]TerminalSkillState
	skillTTL time.Duration
}

func NewRegistry(skillTTL time.Duration) *Registry {
	if skillTTL <= 0 {
		skillTTL = 60 * time.Second
	}
	return &Registry{
		data:     make(map[string]TerminalSkillState),
		skillTTL: skillTTL,
	}
}

func (r *Registry) SetSkills(terminalID, soulID string, skillVersion int64, skills []domain.SkillDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.data[terminalID]
	// Only accept newer skill versions once the terminal reports a versioned snapshot.
	if current.SkillVersion > 0 && skillVersion > 0 && skillVersion < current.SkillVersion {
		return
	}
	if current.SkillVersion > 0 && skillVersion == 0 {
		return
	}
	if skillVersion == 0 {
		skillVersion = current.SkillVersion
	}

	r.data[terminalID] = TerminalSkillState{
		TerminalID:     terminalID,
		SoulID:         soulID,
		SkillVersion:   skillVersion,
		Skills:         skills,
		CatalogVersion: current.CatalogVersion,
		IntentCatalog:  append([]domain.IntentSpec{}, current.IntentCatalog...),
		Online:         true,
		LastUpdated:    time.Now(),
	}
}

func (r *Registry) SetIntentCatalog(terminalID, soulID string, catalogVersion int64, catalog []domain.IntentSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()

	current := r.data[terminalID]
	if current.CatalogVersion > 0 && catalogVersion > 0 && catalogVersion < current.CatalogVersion {
		return
	}
	if current.CatalogVersion > 0 && catalogVersion == 0 {
		return
	}
	if catalogVersion == 0 {
		catalogVersion = current.CatalogVersion
	}

	r.data[terminalID] = TerminalSkillState{
		TerminalID:     terminalID,
		SoulID:         soulID,
		SkillVersion:   current.SkillVersion,
		Skills:         append([]domain.SkillDefinition{}, current.Skills...),
		CatalogVersion: catalogVersion,
		IntentCatalog:  append([]domain.IntentSpec{}, catalog...),
		Online:         true,
		LastUpdated:    time.Now(),
	}
}

func (r *Registry) SetOnline(terminalID string, online bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.data[terminalID]
	state.Online = online
	state.LastUpdated = time.Now()
	r.data[terminalID] = state
}

func (r *Registry) SetSoul(terminalID, soulID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := r.data[terminalID]
	state.TerminalID = terminalID
	state.SoulID = soulID
	state.LastUpdated = time.Now()
	r.data[terminalID] = state
}

func (r *Registry) GetState(terminalID string) (TerminalSkillState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.data[terminalID]
	if !ok || r.isExpired(state) {
		return TerminalSkillState{}, false
	}
	out := state
	out.Skills = make([]domain.SkillDefinition, len(state.Skills))
	copy(out.Skills, state.Skills)
	out.IntentCatalog = make([]domain.IntentSpec, len(state.IntentCatalog))
	copy(out.IntentCatalog, state.IntentCatalog)
	return out, true
}

func (r *Registry) GetSkills(terminalID string) []domain.SkillDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.data[terminalID]
	if !ok || !state.Online || r.isExpired(state) {
		return nil
	}

	out := make([]domain.SkillDefinition, len(state.Skills))
	copy(out, state.Skills)
	return out
}

func (r *Registry) GetIntentCatalog(terminalID string) []domain.IntentSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.data[terminalID]
	if !ok || !state.Online || r.isExpired(state) {
		return nil
	}
	out := make([]domain.IntentSpec, len(state.IntentCatalog))
	copy(out, state.IntentCatalog)
	return out
}

func (r *Registry) ListOnlineStates() []TerminalSkillState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]TerminalSkillState, 0, len(r.data))
	for _, state := range r.data {
		if strings.TrimSpace(state.TerminalID) == "" {
			continue
		}
		if !state.Online || r.isExpired(state) {
			continue
		}
		item := state
		item.Skills = append([]domain.SkillDefinition{}, state.Skills...)
		item.IntentCatalog = append([]domain.IntentSpec{}, state.IntentCatalog...)
		out = append(out, item)
	}
	return out
}

func (r *Registry) isExpired(state TerminalSkillState) bool {
	if r.skillTTL <= 0 {
		return false
	}
	return time.Since(state.LastUpdated) > r.skillTTL
}
