package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"soul/internal/domain"
)

var (
	ErrSoulNotFound          = errors.New("soul not found")
	ErrSoulSelectionRequired = errors.New("soul selection is required before chat")
)

type Store struct {
	pool *pgxpool.Pool
}

type MessageChunk struct {
	ID      int64
	Role    string
	Content string
}

type SessionCompactionState struct {
	Summary                string
	LastCompactedMessageID int64
}

type SessionCompactionStats struct {
	MessageCount int
	CharCount    int
}

type IdleSession struct {
	SessionID        string
	UserID           string
	TerminalID       string
	SoulID           string
	LastUserActiveAt time.Time
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL UNIQUE,
			user_uuid TEXT NOT NULL UNIQUE DEFAULT (
				substr(md5(random()::text || clock_timestamp()::text), 1, 8) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 12)
			),
			display_name TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE TABLE IF NOT EXISTS souls (
			soul_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			mbti_type TEXT NOT NULL DEFAULT 'INTJ',
			personality_vector JSONB NOT NULL DEFAULT '{"empathy":0.5,"sensitivity":0.5,"stability":0.5,"expressiveness":0.5,"dominance":0.5}'::jsonb,
			emotion_state JSONB NOT NULL DEFAULT '{"p":0,"a":0,"d":0,"boredom":0,"shock_load":0,"extreme_memory":0,"long_mu_p":0,"long_mu_a":0,"long_mu_d":0,"long_volatility":0,"drift":{"empathy":0,"sensitivity":0,"stability":0,"expressiveness":0,"dominance":0},"last_interaction_at":"1970-01-01T00:00:00Z","last_updated_at":"1970-01-01T00:00:00Z"}'::jsonb,
			model_version TEXT NOT NULL DEFAULT 'persona-pad-v2',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (user_id, name)
		);`,
		`INSERT INTO users(user_id, display_name)
		VALUES ('demo-user', 'demo-user')
		ON CONFLICT (user_id) DO NOTHING;`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM souls
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`CREATE TABLE IF NOT EXISTS terminal_soul_bindings (
			user_id TEXT NOT NULL,
			terminal_id TEXT NOT NULL,
			soul_id TEXT NOT NULL REFERENCES souls(soul_id) ON DELETE RESTRICT,
			first_bound_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, terminal_id)
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			terminal_id TEXT NOT NULL,
			soul_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			terminal_id TEXT NOT NULL,
			soul_id TEXT NOT NULL,
			role TEXT NOT NULL,
			name TEXT,
			tool_call_id TEXT,
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_soul_created ON messages(soul_id, created_at);`,
		`CREATE TABLE IF NOT EXISTS memory_episode (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			terminal_id TEXT NOT NULL,
			soul_id TEXT NOT NULL,
			summary TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_memory_episode_soul_created ON memory_episode(soul_id, created_at);`,
		`ALTER TABLE souls ADD COLUMN IF NOT EXISTS mbti_type TEXT NOT NULL DEFAULT 'INTJ';`,
		`ALTER TABLE souls ADD COLUMN IF NOT EXISTS personality_vector JSONB NOT NULL DEFAULT '{"empathy":0.5,"sensitivity":0.5,"stability":0.5,"expressiveness":0.5,"dominance":0.5}'::jsonb;`,
		`ALTER TABLE souls ADD COLUMN IF NOT EXISTS emotion_state JSONB NOT NULL DEFAULT '{"p":0,"a":0,"d":0,"boredom":0,"shock_load":0,"extreme_memory":0,"long_mu_p":0,"long_mu_a":0,"long_mu_d":0,"long_volatility":0,"drift":{"empathy":0,"sensitivity":0,"stability":0,"expressiveness":0,"dominance":0},"last_interaction_at":"1970-01-01T00:00:00Z","last_updated_at":"1970-01-01T00:00:00Z"}'::jsonb;`,
		`ALTER TABLE souls ADD COLUMN IF NOT EXISTS model_version TEXT NOT NULL DEFAULT 'persona-pad-v2';`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
		`ALTER TABLE memory_episode ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS summary_updated_at TIMESTAMPTZ;`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_compacted_message_id BIGINT NOT NULL DEFAULT 0;`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_user_active_at TIMESTAMPTZ;`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS idle_processed_at TIMESTAMPTZ;`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_last_user_active ON sessions(last_user_active_at);`,
		`ALTER TABLE memory_episode ADD COLUMN IF NOT EXISTS session_id TEXT;`,
		`CREATE TABLE IF NOT EXISTS mem0_async_jobs (
			id BIGSERIAL PRIMARY KEY,
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			terminal_id TEXT NOT NULL,
			soul_id TEXT NOT NULL,
			summary TEXT NOT NULL,
			trigger_source TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`,
		`CREATE INDEX IF NOT EXISTS idx_mem0_async_jobs_status_created ON mem0_async_jobs(status, created_at);`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM sessions
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM messages
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM memory_episode
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM terminal_soul_bindings
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`INSERT INTO users(user_id, display_name)
		SELECT DISTINCT user_id, user_id
		FROM mem0_async_jobs
		WHERE COALESCE(TRIM(user_id), '') <> ''
		ON CONFLICT (user_id) DO NOTHING;`,
		`CREATE TABLE IF NOT EXISTS soul_user_relations (
			id BIGSERIAL PRIMARY KEY,
			relation_uuid TEXT NOT NULL UNIQUE DEFAULT (
				substr(md5(random()::text || clock_timestamp()::text), 1, 8) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 4) || '-' ||
				substr(md5(random()::text || clock_timestamp()::text), 1, 12)
			),
			soul_id TEXT NOT NULL REFERENCES souls(soul_id) ON DELETE CASCADE,
			related_user_id TEXT REFERENCES users(user_id) ON DELETE SET NULL,
			appellation TEXT NOT NULL,
			relation_to_owner TEXT NOT NULL,
			user_description TEXT NOT NULL DEFAULT '',
			personality_model JSONB,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (soul_id, appellation)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_soul_user_relations_soul_created ON soul_user_relations(soul_id, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_soul_user_relations_related_user ON soul_user_relations(related_user_id);`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_souls_user_id_users'
			) THEN
				ALTER TABLE souls
				ADD CONSTRAINT fk_souls_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE RESTRICT;
			END IF;
		END
		$$;`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_terminal_soul_bindings_user_id_users'
			) THEN
				ALTER TABLE terminal_soul_bindings
				ADD CONSTRAINT fk_terminal_soul_bindings_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE;
			END IF;
		END
		$$;`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_sessions_user_id_users'
			) THEN
				ALTER TABLE sessions
				ADD CONSTRAINT fk_sessions_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE;
			END IF;
		END
		$$;`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_messages_user_id_users'
			) THEN
				ALTER TABLE messages
				ADD CONSTRAINT fk_messages_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE;
			END IF;
		END
		$$;`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_memory_episode_user_id_users'
			) THEN
				ALTER TABLE memory_episode
				ADD CONSTRAINT fk_memory_episode_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE;
			END IF;
		END
		$$;`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'fk_mem0_async_jobs_user_id_users'
			) THEN
				ALTER TABLE mem0_async_jobs
				ADD CONSTRAINT fk_mem0_async_jobs_user_id_users
				FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE;
			END IF;
		END
		$$;`,
	}

	for _, q := range queries {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureUserExists(ctx context.Context, userID string) error {
	trimmed := strings.TrimSpace(userID)
	if trimmed == "" {
		return fmt.Errorf("user_id is required")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users(user_id, display_name)
		VALUES ($1, $1)
		ON CONFLICT (user_id) DO NOTHING;
	`, trimmed)
	return err
}

func (s *Store) CreateUser(ctx context.Context, userID, displayName, description string) (domain.UserProfile, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		userID = "user_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = userID
	}
	description = strings.TrimSpace(description)

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO users(user_id, display_name, description)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO NOTHING;
	`, userID, displayName, description)
	if err != nil {
		return domain.UserProfile{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.UserProfile{}, fmt.Errorf("user_id already exists: %s", userID)
	}
	return s.GetUserByID(ctx, userID)
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (domain.UserProfile, error) {
	userID = strings.TrimSpace(userID)
	var out domain.UserProfile
	var createdAt time.Time
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, user_uuid, display_name, description, created_at, updated_at
		FROM users
		WHERE user_id=$1
	`, userID).Scan(
		&out.ID,
		&out.UserID,
		&out.UserUUID,
		&out.DisplayName,
		&out.Description,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.UserProfile{}, fmt.Errorf("user not found: %s", userID)
	}
	if err != nil {
		return domain.UserProfile{}, err
	}
	out.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	return out, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.UserProfile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, user_uuid, display_name, description, created_at, updated_at
		FROM users
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.UserProfile, 0, 16)
	for rows.Next() {
		var item domain.UserProfile
		var createdAt time.Time
		var updatedAt time.Time
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.UserUUID,
			&item.DisplayName,
			&item.Description,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		item.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ResolveOrCreateSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
	return s.ResolveSoul(ctx, userID, terminalID, soulHint)
}

func (s *Store) ResolveSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return "", err
	}

	if soulID, err := s.getBoundSoul(ctx, userID, terminalID); err != nil {
		return "", err
	} else if soulID != "" {
		return soulID, nil
	}

	resolvedSoulID, err := s.matchExistingSoul(ctx, userID, soulHint)
	if err != nil {
		return "", err
	}

	if resolvedSoulID == "" {
		return "", ErrSoulSelectionRequired
	}

	if err := s.bindTerminalSoul(ctx, userID, terminalID, resolvedSoulID); err != nil {
		return "", err
	}
	return resolvedSoulID, nil
}

func (s *Store) getBoundSoul(ctx context.Context, userID, terminalID string) (string, error) {
	var soulID string
	err := s.pool.QueryRow(ctx, `
		SELECT soul_id
		FROM terminal_soul_bindings
		WHERE user_id=$1 AND terminal_id=$2
	`, userID, terminalID).Scan(&soulID)
	if err == nil {
		_, updErr := s.pool.Exec(ctx, `
			UPDATE terminal_soul_bindings
			SET last_seen_at = NOW()
			WHERE user_id=$1 AND terminal_id=$2
		`, userID, terminalID)
		if updErr != nil {
			return "", updErr
		}
		return soulID, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return "", err
}

func (s *Store) matchExistingSoul(ctx context.Context, userID, soulHint string) (string, error) {
	if soulHint != "" {
		var soulID string
		err := s.pool.QueryRow(ctx, `
			SELECT soul_id
			FROM souls
			WHERE user_id=$1 AND (soul_id=$2 OR name=$2)
			LIMIT 1
		`, userID, soulHint).Scan(&soulID)
		if err == nil {
			return soulID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}

	if soulHint == "" {
		var cnt int
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM souls WHERE user_id=$1`, userID).Scan(&cnt); err != nil {
			return "", err
		}
		if cnt == 1 {
			var soulID string
			if err := s.pool.QueryRow(ctx, `SELECT soul_id FROM souls WHERE user_id=$1 LIMIT 1`, userID).Scan(&soulID); err != nil {
				return "", err
			}
			return soulID, nil
		}
		if cnt == 0 {
			return "", ErrSoulNotFound
		}
		return "", ErrSoulSelectionRequired
	}

	return "", ErrSoulNotFound
}

func (s *Store) bindTerminalSoul(ctx context.Context, userID, terminalID, soulID string) error {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	var ownerUserID string
	if err := s.pool.QueryRow(ctx, `SELECT user_id FROM souls WHERE soul_id=$1`, soulID).Scan(&ownerUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSoulNotFound
		}
		return err
	}
	if strings.TrimSpace(ownerUserID) != strings.TrimSpace(userID) {
		return fmt.Errorf("soul %s does not belong to user %s", soulID, userID)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO terminal_soul_bindings(user_id, terminal_id, soul_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, terminal_id)
		DO UPDATE SET soul_id = EXCLUDED.soul_id, last_seen_at = NOW();
	`, userID, terminalID, soulID)
	return err
}

func (s *Store) BindTerminalSoul(ctx context.Context, userID, terminalID, soulID string) error {
	return s.bindTerminalSoul(ctx, userID, terminalID, soulID)
}

func (s *Store) CreateSoulProfile(ctx context.Context, userID, name, mbtiType string, vector domain.PersonalityVector, state domain.SoulEmotionState, modelVersion string) (domain.SoulProfile, error) {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return domain.SoulProfile{}, err
	}
	soulID := "soul_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if modelVersion == "" {
		modelVersion = "persona-pad-v2"
	}
	vecJSON, err := json.Marshal(vector)
	if err != nil {
		return domain.SoulProfile{}, err
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return domain.SoulProfile{}, err
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO souls(soul_id, user_id, name, mbti_type, personality_vector, emotion_state, model_version)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7)
		ON CONFLICT (user_id, name) DO NOTHING
	`, soulID, userID, name, strings.ToUpper(strings.TrimSpace(mbtiType)), string(vecJSON), string(stateJSON), modelVersion)
	if err != nil {
		return domain.SoulProfile{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.SoulProfile{}, fmt.Errorf("soul name already exists: %s", name)
	}
	return s.GetSoulProfileByID(ctx, soulID)
}

func (s *Store) GetSoulProfileByID(ctx context.Context, soulID string) (domain.SoulProfile, error) {
	var out domain.SoulProfile
	var vectorRaw []byte
	var stateRaw []byte
	var createdAt time.Time
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT soul_id, user_id, name, mbti_type, personality_vector, emotion_state, model_version, created_at, updated_at
		FROM souls
		WHERE soul_id=$1
	`, soulID).Scan(
		&out.SoulID,
		&out.UserID,
		&out.Name,
		&out.MBTIType,
		&vectorRaw,
		&stateRaw,
		&out.ModelVersion,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SoulProfile{}, ErrSoulNotFound
	}
	if err != nil {
		return domain.SoulProfile{}, err
	}
	if err := json.Unmarshal(vectorRaw, &out.PersonalityVector); err != nil {
		return domain.SoulProfile{}, err
	}
	if err := json.Unmarshal(stateRaw, &out.EmotionState); err != nil {
		return domain.SoulProfile{}, err
	}
	out.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	return out, nil
}

func (s *Store) ListSoulProfiles(ctx context.Context, userID string) ([]domain.SoulProfile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT soul_id
		FROM souls
		WHERE user_id=$1
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.SoulProfile
	for rows.Next() {
		var soulID string
		if err := rows.Scan(&soulID); err != nil {
			return nil, err
		}
		item, err := s.GetSoulProfileByID(ctx, soulID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpdateSoulEmotionState(ctx context.Context, soulID string, state domain.SoulEmotionState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE souls
		SET emotion_state=$2::jsonb, updated_at=NOW()
		WHERE soul_id=$1
	`, soulID, string(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSoulNotFound
	}
	return nil
}

func (s *Store) LoadSoulProfilePrompt(ctx context.Context, soulID string) (string, error) {
	p, err := s.GetSoulProfileByID(ctx, soulID)
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf(
		"灵魂画像: MBTI=%s, T=(empathy=%.2f, sensitivity=%.2f, stability=%.2f, expressiveness=%.2f, dominance=%.2f)。当前PAD=(P=%.2f, A=%.2f, D=%.2f)，请保持该灵魂风格并兼顾安全。",
		p.MBTIType,
		p.PersonalityVector.Empathy,
		p.PersonalityVector.Sensitivity,
		p.PersonalityVector.Stability,
		p.PersonalityVector.Expressiveness,
		p.PersonalityVector.Dominance,
		p.EmotionState.P,
		p.EmotionState.A,
		p.EmotionState.D,
	)
	return prompt, nil
}

func (s *Store) SaveMessage(ctx context.Context, sessionID, userID, terminalID, soulID, role, name, toolCallID, content string) error {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions(session_id, user_id, terminal_id, soul_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id)
		DO UPDATE SET user_id=EXCLUDED.user_id, terminal_id=EXCLUDED.terminal_id, soul_id=EXCLUDED.soul_id;
	`, sessionID, userID, terminalID, soulID)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO messages(session_id, user_id, terminal_id, soul_id, role, name, tool_call_id, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, sessionID, userID, terminalID, soulID, role, nullIfEmpty(name), nullIfEmpty(toolCallID), content)
	if err != nil {
		return err
	}

	if role == "user" {
		return s.MarkUserActive(ctx, sessionID, userID, terminalID, soulID, time.Now())
	}
	return nil
}

func (s *Store) GetRecentMessages(ctx context.Context, sessionID string, limit int) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role, COALESCE(content, ''), COALESCE(name, ''), COALESCE(tool_call_id, '')
		FROM (
			SELECT role, content, name, tool_call_id, created_at
			FROM messages
			WHERE session_id=$1 AND role IN ('user', 'assistant', 'tool')
			ORDER BY created_at DESC
			LIMIT $2
		) t
		ORDER BY created_at ASC
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	msgs := make([]domain.Message, 0, limit)
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(&m.Role, &m.Content, &m.Name, &m.ToolCallID); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return msgs, nil
}

func (s *Store) GetRecentEpisodes(ctx context.Context, soulID string, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT summary
		FROM memory_episode
		WHERE soul_id=$1
		ORDER BY created_at DESC
		LIMIT $2
	`, soulID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) MarkUserActive(ctx context.Context, sessionID, userID, terminalID, soulID string, at time.Time) error {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions(session_id, user_id, terminal_id, soul_id, last_user_active_at, idle_processed_at)
		VALUES ($1, $2, $3, $4, $5, NULL)
		ON CONFLICT (session_id)
		DO UPDATE SET
			user_id = EXCLUDED.user_id,
			terminal_id = EXCLUDED.terminal_id,
			soul_id = EXCLUDED.soul_id,
			last_user_active_at = EXCLUDED.last_user_active_at,
			idle_processed_at = NULL;
	`, sessionID, userID, terminalID, soulID, at)
	return err
}

func (s *Store) GetSessionSummary(ctx context.Context, sessionID string) (string, error) {
	var summary string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(summary, '')
		FROM sessions
		WHERE session_id=$1
	`, sessionID).Scan(&summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return summary, nil
}

func (s *Store) GetSessionCompactionState(ctx context.Context, sessionID string) (SessionCompactionState, error) {
	var state SessionCompactionState
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(summary, ''), COALESCE(last_compacted_message_id, 0)
		FROM sessions
		WHERE session_id=$1
	`, sessionID).Scan(&state.Summary, &state.LastCompactedMessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionCompactionState{}, nil
	}
	if err != nil {
		return SessionCompactionState{}, err
	}
	return state, nil
}

func (s *Store) GetSessionCompactionStats(ctx context.Context, sessionID string, lastCompactedMessageID int64) (SessionCompactionStats, error) {
	var stats SessionCompactionStats
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(char_length(content)), 0)
		FROM messages
		WHERE session_id=$1
		  AND id > $2
		  AND role IN ('user', 'assistant', 'tool', 'observation')
	`, sessionID, lastCompactedMessageID).Scan(&stats.MessageCount, &stats.CharCount)
	if err != nil {
		return SessionCompactionStats{}, err
	}
	return stats, nil
}

func (s *Store) GetMessagesSince(ctx context.Context, sessionID string, lastCompactedMessageID int64, limit int) ([]MessageChunk, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, role, COALESCE(content, '')
		FROM messages
		WHERE session_id=$1
		  AND id > $2
		  AND role IN ('user', 'assistant', 'tool', 'observation')
		ORDER BY id ASC
		LIMIT $3
	`, sessionID, lastCompactedMessageID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]MessageChunk, 0, limit)
	for rows.Next() {
		var chunk MessageChunk
		if err := rows.Scan(&chunk.ID, &chunk.Role, &chunk.Content); err != nil {
			return nil, err
		}
		out = append(out, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpdateSessionSummary(ctx context.Context, sessionID, userID, terminalID, soulID, summary string, lastCompactedMessageID int64) error {
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET summary=$2,
			summary_updated_at=NOW(),
			last_compacted_message_id=$3
		WHERE session_id=$1
	`, sessionID, summary, lastCompactedMessageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions(
			session_id, user_id, terminal_id, soul_id, summary, summary_updated_at, last_compacted_message_id
		)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6)
		ON CONFLICT (session_id)
		DO UPDATE SET
			user_id=EXCLUDED.user_id,
			terminal_id=EXCLUDED.terminal_id,
			soul_id=EXCLUDED.soul_id,
			summary=EXCLUDED.summary,
			summary_updated_at=NOW(),
			last_compacted_message_id=EXCLUDED.last_compacted_message_id;
	`, sessionID, userID, terminalID, soulID, summary, lastCompactedMessageID)
	return err
}

func (s *Store) InsertMemoryEpisode(ctx context.Context, sessionID, userID, terminalID, soulID, summary string) error {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memory_episode(session_id, user_id, terminal_id, soul_id, summary)
		VALUES ($1, $2, $3, $4, $5)
	`, sessionID, userID, terminalID, soulID, summary)
	return err
}

func (s *Store) EnqueueMem0AsyncJob(ctx context.Context, sessionID, userID, terminalID, soulID, summary, triggerSource string) error {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	if err := s.ensureUserExists(ctx, userID); err != nil {
		return err
	}
	if triggerSource == "" {
		triggerSource = "idle_timeout"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem0_async_jobs(session_id, user_id, terminal_id, soul_id, summary, trigger_source, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', NOW())
	`, sessionID, userID, terminalID, soulID, summary, triggerSource)
	return err
}

func (s *Store) ListIdleSessionsForSummary(ctx context.Context, idleBefore time.Time, limit int) ([]IdleSession, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT session_id, user_id, terminal_id, soul_id, last_user_active_at
		FROM sessions
		WHERE last_user_active_at IS NOT NULL
		  AND last_user_active_at <= $1
		  AND (idle_processed_at IS NULL OR idle_processed_at < last_user_active_at)
		ORDER BY last_user_active_at ASC
		LIMIT $2
	`, idleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]IdleSession, 0, limit)
	for rows.Next() {
		var item IdleSession
		if err := rows.Scan(&item.SessionID, &item.UserID, &item.TerminalID, &item.SoulID, &item.LastUserActiveAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MarkIdleSummaryProcessed(ctx context.Context, sessionID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions
		SET idle_processed_at=$2
		WHERE session_id=$1
	`, sessionID, at)
	return err
}

func (s *Store) BuildMemoryContext(ctx context.Context, soulID string) (string, error) {
	profile, err := s.LoadSoulProfilePrompt(ctx, soulID)
	if err != nil {
		return "", err
	}
	episodes, err := s.GetRecentEpisodes(ctx, soulID, 3)
	if err != nil {
		return "", err
	}

	if len(episodes) == 0 {
		return profile, nil
	}

	return profile + "\n近期片段记忆:\n- " + strings.Join(episodes, "\n- "), nil
}

func (s *Store) CreateSoulUserRelation(ctx context.Context, soulID string, payload domain.CreateSoulUserRelationPayload) (domain.SoulUserRelation, error) {
	soulID = strings.TrimSpace(soulID)
	if soulID == "" {
		return domain.SoulUserRelation{}, fmt.Errorf("soul_id is required")
	}
	appellation := strings.TrimSpace(payload.Appellation)
	if appellation == "" {
		return domain.SoulUserRelation{}, fmt.Errorf("appellation is required")
	}
	relationToOwner := strings.TrimSpace(payload.RelationToOwner)
	if relationToOwner == "" {
		return domain.SoulUserRelation{}, fmt.Errorf("relation_to_owner is required")
	}
	relatedUserID := strings.TrimSpace(payload.RelatedUserID)
	if relatedUserID != "" {
		if _, err := s.GetUserByID(ctx, relatedUserID); err != nil {
			return domain.SoulUserRelation{}, err
		}
	}

	var personalityJSON any
	if payload.PersonalityModel != nil {
		raw, err := json.Marshal(payload.PersonalityModel)
		if err != nil {
			return domain.SoulUserRelation{}, err
		}
		personalityJSON = string(raw)
	}

	var out domain.SoulUserRelation
	var personalityRaw []byte
	var createdAt time.Time
	var updatedAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO soul_user_relations(
			soul_id, related_user_id, appellation, relation_to_owner, user_description, personality_model
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id, relation_uuid, soul_id, COALESCE(related_user_id, ''), appellation, relation_to_owner, user_description, personality_model, created_at, updated_at
	`,
		soulID,
		nullIfEmpty(relatedUserID),
		appellation,
		relationToOwner,
		strings.TrimSpace(payload.UserDescription),
		personalityJSON,
	).Scan(
		&out.ID,
		&out.RelationUUID,
		&out.SoulID,
		&out.RelatedUserID,
		&out.Appellation,
		&out.RelationToOwner,
		&out.UserDescription,
		&personalityRaw,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return domain.SoulUserRelation{}, err
	}
	if len(personalityRaw) > 0 {
		var model domain.PersonalityVector
		if err := json.Unmarshal(personalityRaw, &model); err != nil {
			return domain.SoulUserRelation{}, err
		}
		out.PersonalityModel = &model
	}
	out.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
	out.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
	return out, nil
}

func (s *Store) ListSoulUserRelations(ctx context.Context, soulID string) ([]domain.SoulUserRelation, error) {
	soulID = strings.TrimSpace(soulID)
	if soulID == "" {
		return nil, fmt.Errorf("soul_id is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, relation_uuid, soul_id, COALESCE(related_user_id, ''), appellation, relation_to_owner, user_description, personality_model, created_at, updated_at
		FROM soul_user_relations
		WHERE soul_id=$1
		ORDER BY created_at ASC
	`, soulID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.SoulUserRelation, 0, 8)
	for rows.Next() {
		var item domain.SoulUserRelation
		var personalityRaw []byte
		var createdAt time.Time
		var updatedAt time.Time
		if err := rows.Scan(
			&item.ID,
			&item.RelationUUID,
			&item.SoulID,
			&item.RelatedUserID,
			&item.Appellation,
			&item.RelationToOwner,
			&item.UserDescription,
			&personalityRaw,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		if len(personalityRaw) > 0 {
			var model domain.PersonalityVector
			if err := json.Unmarshal(personalityRaw, &model); err != nil {
				return nil, err
			}
			item.PersonalityModel = &model
		}
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		item.UpdatedAt = updatedAt.UTC().Format(time.RFC3339Nano)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
