package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"codeck/internal/balances"
	"codeck/internal/store"
)

type balanceListResponse struct {
	Available bool                  `json:"available"`
	Error     string                `json:"error,omitempty"`
	Balances  []store.BalanceConfig `json:"balances"`
}

type balanceInput struct {
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	Key             string `json:"key"`
	IntervalSeconds int    `json:"interval_seconds"`
}

func (s *Server) handleListBalances(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.ListBalanceConfigs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取余额配置失败")
		return
	}
	if rows == nil {
		rows = []store.BalanceConfig{}
	}
	writeJSON(w, http.StatusOK, balanceListResponse{
		Available: s.balances != nil && s.balances.Available(),
		Error:     availabilityError(s.balances),
		Balances:  rows,
	})
}

func (s *Server) handleCreateBalance(w http.ResponseWriter, r *http.Request) {
	if !s.balanceFeatureAvailable(w) {
		return
	}
	var req balanceInput
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Key) == "" {
		writeError(w, http.StatusBadRequest, "Key 不能为空")
		return
	}
	if req.IntervalSeconds == 0 {
		req.IntervalSeconds = 300
	}
	if req.IntervalSeconds < 60 {
		writeError(w, http.StatusBadRequest, "轮询周期至少为 1 分钟")
		return
	}
	ciphertext, err := s.balances.EncryptCredential(strings.TrimSpace(req.Key))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "余额凭据加密不可用")
		return
	}
	created, err := s.db.CreateBalanceConfig(store.BalanceConfig{
		Provider: req.Provider, Name: req.Name, IntervalSeconds: req.IntervalSeconds,
		CredentialCiphertext: ciphertext,
	})
	if err != nil {
		if errors.Is(err, store.ErrBalanceInvalid) {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "保存余额配置失败")
		return
	}
	s.balances.ScheduleUpdate(created.ID, true)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateBalance(w http.ResponseWriter, r *http.Request) {
	if !s.balanceFeatureAvailable(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	old, err := s.db.GetBalanceConfig(id)
	if err != nil {
		writeStoreError(w, err, "余额配置")
		return
	}
	var req balanceInput
	if !decodeJSON(w, r, &req) {
		return
	}
	replaceKey := strings.TrimSpace(req.Key) != ""
	if req.Provider != old.Provider && !replaceKey {
		writeError(w, http.StatusBadRequest, "更换平台时需要填写该平台的 Key")
		return
	}
	if replaceKey {
		ciphertext, err := s.balances.EncryptCredential(strings.TrimSpace(req.Key))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "余额凭据加密不可用")
			return
		}
		old.CredentialCiphertext = ciphertext
	}
	updated, err := s.db.UpdateBalanceConfig(store.BalanceConfig{
		ID: id, Provider: req.Provider, Name: req.Name,
		IntervalSeconds: req.IntervalSeconds, CredentialCiphertext: old.CredentialCiphertext,
	}, replaceKey)
	if err != nil {
		if errors.Is(err, store.ErrBalanceInvalid) {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		writeStoreError(w, err, "余额配置")
		return
	}
	s.balances.ScheduleUpdate(id, replaceKey)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteBalance(w http.ResponseWriter, r *http.Request) {
	if !s.balanceFeatureAvailable(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteBalanceConfig(id); err != nil {
		writeStoreError(w, err, "余额配置")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRefreshBalance(w http.ResponseWriter, r *http.Request) {
	if !s.balanceFeatureAvailable(w) {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.balances.RefreshNow(ctx, id); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		// Provider errors are safely persisted on the row and surfaced there; a
		// successful HTTP response lets the UI keep showing the previous balance.
	}
	updated, err := s.db.GetBalanceConfig(id)
	if err != nil {
		writeStoreError(w, err, "余额配置")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) balanceFeatureAvailable(w http.ResponseWriter) bool {
	if s.balances == nil || !s.balances.Available() {
		writeError(w, http.StatusServiceUnavailable, "%s", availabilityError(s.balances))
		return false
	}
	return true
}

func availabilityError(manager *balances.Manager) string {
	if manager == nil {
		return "余额服务未启动"
	}
	return manager.AvailabilityError()
}
