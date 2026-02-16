package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"soul/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
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
		`CREATE TABLE IF NOT EXISTS souls (
			soul_id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			mbti_ei INT NOT NULL DEFAULT 10,
			mbti_sn INT NOT NULL DEFAULT 10,
			mbti_tf INT NOT NULL DEFAULT 10,
			mbti_jp INT NOT NULL DEFAULT 10,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (user_id, name)
		);`,
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
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
		`ALTER TABLE memory_episode ADD COLUMN IF NOT EXISTS soul_id TEXT;`,
	}

	for _, q := range queries {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ResolveOrCreateSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
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
		resolvedSoulID, err = s.createSoul(ctx, userID, terminalID, soulHint)
		if err != nil {
			return "", err
		}
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
	}

	return "", nil
}

func (s *Store) createSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
	baseName := soulHint
	if baseName == "" {
		baseName = fmt.Sprintf("%s-soul", terminalID)
	}

	for i := range 5 {
		soulID := "soul_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		name := baseName
		if i > 0 {
			name = fmt.Sprintf("%s-%d", baseName, i+1)
		}

		tag, err := s.pool.Exec(ctx, `
			INSERT INTO souls(soul_id, user_id, name)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, name) DO NOTHING
		`, soulID, userID, name)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 1 {
			return soulID, nil
		}
	}

	return "", fmt.Errorf("create soul failed due to name conflicts")
}

func (s *Store) bindTerminalSoul(ctx context.Context, userID, terminalID, soulID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO terminal_soul_bindings(user_id, terminal_id, soul_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, terminal_id)
		DO UPDATE SET soul_id = EXCLUDED.soul_id, last_seen_at = NOW();
	`, userID, terminalID, soulID)
	return err
}

func (s *Store) LoadSoulProfilePrompt(ctx context.Context, soulID string) (string, error) {
	var ei, sn, tf, jp int
	err := s.pool.QueryRow(ctx, `
		SELECT mbti_ei, mbti_sn, mbti_tf, mbti_jp
		FROM souls
		WHERE soul_id=$1
	`, soulID).Scan(&ei, &sn, &tf, &jp)
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf(
		"灵魂画像(MBTI数值): EI=%d, SN=%d, TF=%d, JP=%d。回复风格要与该灵魂一致。",
		ei, sn, tf, jp,
	)
	return prompt, nil
}

func (s *Store) SaveMessage(ctx context.Context, sessionID, userID, terminalID, soulID, role, name, toolCallID, content string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions(session_id, user_id, terminal_id, soul_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id) DO NOTHING;
	`, sessionID, userID, terminalID, soulID)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO messages(session_id, user_id, terminal_id, soul_id, role, name, tool_call_id, content)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, sessionID, userID, terminalID, soulID, role, nullIfEmpty(name), nullIfEmpty(toolCallID), content)
	return err
}

func (s *Store) GetRecentMessages(ctx context.Context, sessionID string, limit int) ([]domain.Message, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role, COALESCE(content, ''), COALESCE(name, ''), COALESCE(tool_call_id, '')
		FROM (
			SELECT role, content, name, tool_call_id, created_at
			FROM messages
			WHERE session_id=$1
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
