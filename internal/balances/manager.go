// Package balances reads provider account balances independently of Codex
// profiles, conversations, and token usage.
package balances

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"codeck/internal/store"
)

type repository interface {
	ListBalanceConfigs() ([]store.BalanceConfig, error)
	GetBalanceConfig(int64) (store.BalanceConfig, error)
	SaveBalanceSuccess(int64, int64, []store.BalanceAmount, time.Time) error
	SaveBalanceError(int64, int64, string) error
}

type fetcher interface {
	Fetch(context.Context, string, string) ([]store.BalanceAmount, error)
}

type scheduleUpdate struct {
	id        int64
	immediate bool
}

// Manager polls each configured provider balance on its own interval.
type Manager struct {
	db      repository
	key     []byte
	keyErr  error
	fetcher fetcher
	log     *slog.Logger

	mu       sync.Mutex
	inFlight map[int64]bool
	wake     chan scheduleUpdate
	started  sync.Once
	stateMu  sync.RWMutex
	stateErr error
}

// NewManager treats a missing or invalid encryption key as a disabled balance
// feature, leaving the rest of the application operational.
func NewManager(db repository, encodedKey string, log *slog.Logger) *Manager {
	key, keyErr := parseKey(encodedKey)
	if log == nil {
		log = slog.Default()
	}
	manager := &Manager{
		db: db, key: key, keyErr: keyErr,
		fetcher: httpProvider{client: &http.Client{
			Timeout: providerTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}},
		log: log, inFlight: make(map[int64]bool), wake: make(chan scheduleUpdate, 128),
	}
	if keyErr == nil && db != nil {
		configs, err := db.ListBalanceConfigs()
		if err != nil {
			manager.stateErr = errEncryptionUnavailable
			log.Error("cannot verify stored provider credentials", "error", err)
		} else {
			for _, config := range configs {
				if _, err := open(key, config.CredentialCiphertext); err != nil {
					manager.stateErr = errEncryptionUnavailable
					log.Error("cannot decrypt a stored provider credential", "config_id", config.ID)
					break
				}
			}
		}
	}
	return manager
}

// Available reports whether credentials can be encrypted and decrypted.
func (m *Manager) Available() bool {
	if m.keyErr != nil {
		return false
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.stateErr == nil
}

// AvailabilityError is safe to return to the UI and never includes key data.
func (m *Manager) AvailabilityError() string {
	if m.keyErr != nil {
		return errEncryptionUnavailable.Error()
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	if m.stateErr != nil {
		return errEncryptionUnavailable.Error()
	}
	return ""
}

// EncryptCredential encrypts a provider key for local SQLite storage.
func (m *Manager) EncryptCredential(credential string) ([]byte, error) {
	if !m.Available() {
		return nil, errEncryptionUnavailable
	}
	return seal(m.key, []byte(credential))
}

// Start schedules a startup refresh, then handles per-config polling deadlines.
func (m *Manager) Start(ctx context.Context) {
	m.started.Do(func() {
		go m.run(ctx)
	})
}

// ScheduleUpdate updates an existing config's schedule; immediate also
// triggers a provider read. Calls are non-blocking and coalesce under load.
func (m *Manager) ScheduleUpdate(id int64, immediate bool) {
	m.wake <- scheduleUpdate{id: id, immediate: immediate}
}

// RefreshNow reads one configured balance synchronously for a manual refresh.
func (m *Manager) RefreshNow(ctx context.Context, id int64) error {
	if !m.Available() {
		return errEncryptionUnavailable
	}
	if !m.begin(id) {
		return errors.New("该余额配置正在查询")
	}
	defer m.finish(id)
	return m.refresh(ctx, id)
}

func (m *Manager) run(ctx context.Context) {
	configs, err := m.db.ListBalanceConfigs()
	if err != nil {
		m.log.Error("failed to load balance configurations", "error", err)
		return
	}
	if m.keyErr == nil {
		for _, c := range configs {
			if _, err := open(m.key, c.CredentialCiphertext); err != nil {
				m.stateMu.Lock()
				m.stateErr = errEncryptionUnavailable
				m.stateMu.Unlock()
				m.log.Error("cannot decrypt a stored provider credential", "config_id", c.ID)
				return
			}
		}
	}
	next := make(map[int64]time.Time, len(configs))
	if m.Available() {
		for _, c := range configs {
			next[c.ID] = time.Now()
		}
	}
	for {
		wait := time.Hour
		now := time.Now()
		for _, due := range next {
			if d := time.Until(due); d < wait {
				wait = max(d, 0)
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case update := <-m.wake:
			timer.Stop()
			config, err := m.db.GetBalanceConfig(update.id)
			if err != nil {
				delete(next, update.id)
				continue
			}
			due := time.Now().Add(time.Duration(config.IntervalSeconds) * time.Second)
			if update.immediate && m.Available() {
				due = time.Now()
			}
			next[update.id] = due
		case <-timer.C:
			now = time.Now()
			for id, due := range next {
				if due.After(now) {
					continue
				}
				config, err := m.db.GetBalanceConfig(id)
				if err != nil {
					delete(next, id)
					continue
				}
				next[id] = now.Add(time.Duration(config.IntervalSeconds) * time.Second)
				if !m.begin(id) {
					next[id] = now.Add(time.Second)
					continue
				}
				go func(id int64) {
					defer m.finish(id)
					if err := m.refresh(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
						m.log.Warn("balance refresh failed", "config_id", id, "error", err)
					}
				}(id)
			}
		}
	}
}

func (m *Manager) begin(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight[id] {
		return false
	}
	m.inFlight[id] = true
	return true
}

func (m *Manager) finish(id int64) {
	m.mu.Lock()
	delete(m.inFlight, id)
	m.mu.Unlock()
}

func (m *Manager) refresh(ctx context.Context, id int64) error {
	if !m.Available() {
		return errEncryptionUnavailable
	}
	c, err := m.db.GetBalanceConfig(id)
	if err != nil {
		return err
	}
	key, err := open(m.key, c.CredentialCiphertext)
	if err != nil {
		message := errEncryptionUnavailable.Error()
		_ = m.db.SaveBalanceError(id, c.CredentialVersion, message)
		return errors.New(message)
	}
	balances, err := m.fetcher.Fetch(ctx, c.Provider, string(key))
	if err != nil {
		if saveErr := m.db.SaveBalanceError(id, c.CredentialVersion, err.Error()); errors.Is(saveErr, store.ErrBalanceCredentialChanged) {
			return nil
		}
		return err
	}
	if err := m.db.SaveBalanceSuccess(id, c.CredentialVersion, balances, time.Now()); err != nil {
		if errors.Is(err, store.ErrBalanceCredentialChanged) || errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("保存余额失败")
	}
	return nil
}
