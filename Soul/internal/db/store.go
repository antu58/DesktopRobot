package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"soul/internal/domain"
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
