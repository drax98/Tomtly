package store

import (
	"context"
	"slices"
	"strings"
	"time"
)

type memorySession struct {
	userID        string
	created, seen time.Time
}

type memoryToken struct {
	Token
	hash string
}

func (m *Memory) userIndex(id string) int {
	return slices.IndexFunc(m.users, func(u User) bool { return u.ID == id })
}

func (m *Memory) nameTaken(name string) bool {
	return slices.ContainsFunc(m.users, func(u User) bool { return strings.EqualFold(u.Username, name) })
}

func (m *Memory) CreateFirstUser(_ context.Context, user User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if len(m.users) > 0 {
		return ErrExists
	}
	m.users = append(m.users, user)
	return nil
}

func (m *Memory) CreateUser(_ context.Context, user User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if m.nameTaken(user.Username) {
		return ErrExists
	}
	m.users = append(m.users, user)
	return nil
}

func (m *Memory) UserByName(_ context.Context, username string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return User{}, m.Fail
	}
	for _, u := range m.users {
		if strings.EqualFold(u.Username, username) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *Memory) UserByID(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return User{}, m.Fail
	}
	if i := m.userIndex(id); i >= 0 {
		return m.users[i], nil
	}
	return User{}, ErrNotFound
}

func (m *Memory) ListUsers(context.Context) ([]User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := slices.Clone(m.users)
	slices.SortStableFunc(out, func(a, b User) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Username, b.Username)
	})
	return out, nil
}

// otherAdmins counts enabled admins other than id.
func (m *Memory) otherAdmins(id string) int {
	n := 0
	for _, u := range m.users {
		if u.ID != id && u.Role == "admin" && !u.Disabled {
			n++
		}
	}
	return n
}

func (m *Memory) dropSessions(userID, keep string) {
	for hash, s := range m.sessions {
		if s.userID == userID && hash != keep {
			delete(m.sessions, hash)
		}
	}
}

func (m *Memory) UpdateUser(_ context.Context, id string, c UserChange) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return User{}, m.Fail
	}
	i := m.userIndex(id)
	if i < 0 {
		return User{}, ErrNotFound
	}
	u := m.users[i]
	wasAdmin := u.Role == "admin" && !u.Disabled
	if c.Role != nil {
		u.Role = *c.Role
	}
	if c.Disabled != nil {
		u.Disabled = *c.Disabled
	}
	if c.Hash != nil {
		u.Hash = *c.Hash
	}
	if wasAdmin && (u.Role != "admin" || u.Disabled) && m.otherAdmins(id) == 0 {
		return User{}, ErrLastAdmin
	}
	m.users[i] = u
	if c.Role != nil || c.Disabled != nil || c.Hash != nil {
		m.dropSessions(id, c.KeepSession)
	}
	if c.DropTokens {
		m.tokens = slices.DeleteFunc(m.tokens, func(t memoryToken) bool { return t.UserID == id })
	}
	return u, nil
}

func (m *Memory) DeleteUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	i := m.userIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	if u := m.users[i]; u.Role == "admin" && !u.Disabled && m.otherAdmins(id) == 0 {
		return ErrLastAdmin
	}
	m.users = slices.Delete(m.users, i, i+1)
	m.dropSessions(id, "")
	m.tokens = slices.DeleteFunc(m.tokens, func(t memoryToken) bool { return t.UserID == id })
	return nil
}

func (m *Memory) CreateSession(_ context.Context, hash, userID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if m.userIndex(userID) < 0 {
		return ErrNotFound
	}
	m.sessions[hash] = memorySession{userID, now, now}
	return nil
}

func dead(s memorySession, now time.Time, idle, absolute time.Duration) bool {
	return now.Sub(s.created) >= absolute || now.Sub(s.seen) >= idle
}

func (m *Memory) SessionUser(_ context.Context, hash string, now time.Time, idle, absolute time.Duration) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return User{}, m.Fail
	}
	s, ok := m.sessions[hash]
	if !ok || dead(s, now, idle, absolute) {
		return User{}, ErrNotFound
	}
	s.seen = now
	m.sessions[hash] = s
	return m.users[m.userIndex(s.userID)], nil
}

func (m *Memory) DeleteSession(_ context.Context, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	delete(m.sessions, hash)
	return nil
}

func (m *Memory) DeleteExpiredSessions(_ context.Context, now time.Time, idle, absolute time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	for hash, s := range m.sessions {
		if dead(s, now, idle, absolute) {
			delete(m.sessions, hash)
		}
	}
	return nil
}

func (m *Memory) CreateToken(_ context.Context, token Token, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	if m.userIndex(token.UserID) < 0 {
		return ErrNotFound
	}
	m.tokens = append(m.tokens, memoryToken{token, hash})
	return nil
}

func (m *Memory) withOwner(t Token) Token {
	t.Owner = m.users[m.userIndex(t.UserID)].Username
	return t
}

func (m *Memory) TokenUser(_ context.Context, hash string, now time.Time) (Token, User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return Token{}, User{}, m.Fail
	}
	for i, t := range m.tokens {
		if t.hash == hash {
			m.tokens[i].LastUsed = now
			return m.withOwner(m.tokens[i].Token), m.users[m.userIndex(t.UserID)], nil
		}
	}
	return Token{}, User{}, ErrNotFound
}

func (m *Memory) ListTokens(_ context.Context, userID string) ([]Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := []Token{}
	for _, t := range m.tokens {
		if userID == "" || t.UserID == userID {
			out = append(out, m.withOwner(t.Token))
		}
	}
	return out, nil
}

func (m *Memory) DeleteToken(_ context.Context, id, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	n := len(m.tokens)
	m.tokens = slices.DeleteFunc(m.tokens, func(t memoryToken) bool { return t.ID == id && (userID == "" || t.UserID == userID) })
	if len(m.tokens) == n {
		return ErrNotFound
	}
	return nil
}

func (m *Memory) AppendAudit(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return m.Fail
	}
	m.audit = append(m.audit, e)
	if len(m.audit) > maxAudit {
		m.audit = slices.Clone(m.audit[len(m.audit)-maxAudit:])
	}
	return nil
}

func (m *Memory) RecentAudit(_ context.Context, limit int) ([]AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		return nil, m.Fail
	}
	out := []AuditEntry{}
	for i := len(m.audit) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, m.audit[i])
	}
	return out, nil
}
