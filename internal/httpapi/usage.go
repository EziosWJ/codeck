package httpapi

import (
	"net/http"
	"time"

	"codeck/internal/store"
	"codeck/internal/usage"
)

const usageCacheTTL = 30 * time.Second

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "1"
	writeJSON(w, http.StatusOK, s.loadUsage(refresh))
}

func (s *Server) loadUsage(refresh bool) usage.Report {
	if err := usage.EnsureSeed(s.db); err != nil {
		s.log.Warn("price seed failed", "error", err)
	}

	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if !refresh && !s.usageCachedAt.IsZero() && time.Since(s.usageCachedAt) < usageCacheTTL {
		return s.usageCached
	}

	homes := usage.Homes(s.cfg.CodexHomeRoot, s.cfg.AuthSource)
	scan := usage.Scan(homes)
	prices, err := s.db.ListModelPrices()
	if err != nil {
		scan.Errors = append(scan.Errors, "list prices: "+err.Error())
		prices = nil
	}
	rep := usage.Summarize(scan.Turns, prices, time.Local, time.Now())
	rep.Roots = scan.Roots
	rep.Files = scan.Files
	rep.Errors = scan.Errors
	s.usageCached = rep
	s.usageCachedAt = time.Now()
	return rep
}

func (s *Server) handleListPrices(w http.ResponseWriter, r *http.Request) {
	if err := usage.EnsureSeed(s.db); err != nil {
		writeError(w, http.StatusInternalServerError, "seed prices: %v", err)
		return
	}
	prices, err := s.db.ListModelPrices()
	if err != nil {
		writeStoreError(w, err, "list prices")
		return
	}
	if prices == nil {
		prices = []store.ModelPrice{}
	}
	writeJSON(w, http.StatusOK, prices)
}

type priceRequest struct {
	Pattern                   string   `json:"pattern"`
	InputUSDPerMTok           float64  `json:"input_usd_per_mtok"`
	CachedInputUSDPerMTok     float64  `json:"cached_input_usd_per_mtok"`
	CacheWriteUSDPerMTok      float64  `json:"cache_write_usd_per_mtok"`
	OutputUSDPerMTok          float64  `json:"output_usd_per_mtok"`
	LongInputUSDPerMTok       *float64 `json:"long_input_usd_per_mtok"`
	LongCachedInputUSDPerMTok *float64 `json:"long_cached_input_usd_per_mtok"`
	LongCacheWriteUSDPerMTok  *float64 `json:"long_cache_write_usd_per_mtok"`
	LongOutputUSDPerMTok      *float64 `json:"long_output_usd_per_mtok"`
	LongThresholdTokens       int      `json:"long_threshold_tokens"`
	Priority                  int      `json:"priority"`
	Notes                     string   `json:"notes"`
}

func (p priceRequest) toStore() store.ModelPrice {
	return store.ModelPrice{
		Pattern:                   p.Pattern,
		InputUSDPerMTok:           p.InputUSDPerMTok,
		CachedInputUSDPerMTok:     p.CachedInputUSDPerMTok,
		CacheWriteUSDPerMTok:      p.CacheWriteUSDPerMTok,
		OutputUSDPerMTok:          p.OutputUSDPerMTok,
		LongInputUSDPerMTok:       p.LongInputUSDPerMTok,
		LongCachedInputUSDPerMTok: p.LongCachedInputUSDPerMTok,
		LongCacheWriteUSDPerMTok:  p.LongCacheWriteUSDPerMTok,
		LongOutputUSDPerMTok:      p.LongOutputUSDPerMTok,
		LongThresholdTokens:       p.LongThresholdTokens,
		Priority:                  p.Priority,
		Notes:                     p.Notes,
	}
}

func (s *Server) handleCreatePrice(w http.ResponseWriter, r *http.Request) {
	var req priceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.db.CreateModelPrice(req.toStore())
	if err != nil {
		writePriceError(w, err, "create price")
		return
	}
	s.invalidateUsage()
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdatePrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req priceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	row := req.toStore()
	row.ID = id
	saved, err := s.db.UpdateModelPrice(row)
	if err != nil {
		writePriceError(w, err, "update price")
		return
	}
	s.invalidateUsage()
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeletePrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteModelPrice(id); err != nil {
		writeStoreError(w, err, "price")
		return
	}
	s.invalidateUsage()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRestorePrices(w http.ResponseWriter, r *http.Request) {
	if err := usage.RestoreDefaults(s.db); err != nil {
		writePriceError(w, err, "restore prices")
		return
	}
	s.invalidateUsage()
	prices, err := s.db.ListModelPrices()
	if err != nil {
		writeStoreError(w, err, "list prices")
		return
	}
	writeJSON(w, http.StatusOK, prices)
}

func (s *Server) invalidateUsage() {
	s.usageMu.Lock()
	s.usageCachedAt = time.Time{}
	s.usageMu.Unlock()
}

func writePriceError(w http.ResponseWriter, err error, what string) {
	if isValidationError(err) {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeStoreError(w, err, what)
}
