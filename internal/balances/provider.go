package balances

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codeck/internal/store"
)

const providerTimeout = 15 * time.Second

type httpProvider struct {
	client        *http.Client
	deepSeekURL   string
	openRouterURL string
}

func (p httpProvider) Fetch(ctx context.Context, name, key string) ([]store.BalanceAmount, error) {
	switch name {
	case "deepseek":
		endpoint := p.deepSeekURL
		if endpoint == "" {
			endpoint = "https://api.deepseek.com/user/balance"
		}
		return p.fetchDeepSeek(ctx, endpoint, key)
	case "openrouter":
		endpoint := p.openRouterURL
		if endpoint == "" {
			endpoint = "https://openrouter.ai/api/v1/credits"
		}
		return p.fetchOpenRouter(ctx, endpoint, key)
	default:
		return nil, fmt.Errorf("不支持的平台")
	}
}

func (p httpProvider) do(ctx context.Context, endpoint, key string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("无法创建平台请求")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("连接平台失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("平台返回 HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("平台返回的数据无法解析")
	}
	return nil
}

func (p httpProvider) fetchDeepSeek(ctx context.Context, endpoint, key string) ([]store.BalanceAmount, error) {
	var result struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency       string `json:"currency"`
			TotalBalance   string `json:"total_balance"`
			GrantedBalance string `json:"granted_balance"`
			ToppedUp       string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := p.do(ctx, endpoint, key, &result); err != nil {
		return nil, err
	}
	if len(result.BalanceInfos) == 0 {
		return nil, fmt.Errorf("DeepSeek 未返回余额")
	}
	balances := make([]store.BalanceAmount, 0, len(result.BalanceInfos))
	for _, info := range result.BalanceInfos {
		if info.Currency != "CNY" && info.Currency != "USD" {
			continue
		}
		if !validDecimal(info.TotalBalance) || !validDecimal(info.GrantedBalance) || !validDecimal(info.ToppedUp) {
			return nil, fmt.Errorf("DeepSeek 返回了无效金额")
		}
		balances = append(balances, store.BalanceAmount{
			Currency: info.Currency,
			Total:    info.TotalBalance,
			Details: map[string]string{
				"granted_balance":   info.GrantedBalance,
				"topped_up_balance": info.ToppedUp,
				"is_available":      fmt.Sprintf("%t", result.IsAvailable),
			},
		})
	}
	if len(balances) == 0 {
		return nil, fmt.Errorf("DeepSeek 未返回支持的币种")
	}
	return balances, nil
}

func (p httpProvider) fetchOpenRouter(ctx context.Context, endpoint, key string) ([]store.BalanceAmount, error) {
	var result struct {
		Data struct {
			TotalCredits json.Number `json:"total_credits"`
			TotalUsage   json.Number `json:"total_usage"`
		} `json:"data"`
	}
	if err := p.do(ctx, endpoint, key, &result); err != nil {
		return nil, err
	}
	total, totalOK := new(big.Rat).SetString(result.Data.TotalCredits.String())
	usage, usageOK := new(big.Rat).SetString(result.Data.TotalUsage.String())
	if !totalOK || !usageOK {
		return nil, fmt.Errorf("OpenRouter 未返回有效余额")
	}
	remaining := new(big.Rat).Sub(total, usage)
	places := max(decimalPlaces(result.Data.TotalCredits.String()), decimalPlaces(result.Data.TotalUsage.String()))
	if places > 10 {
		places = 10
	}
	return []store.BalanceAmount{{Currency: "USD", Total: remaining.FloatString(places)}}, nil
}

func validDecimal(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, ok := new(big.Rat).SetString(value)
	return ok
}

func decimalPlaces(value string) int {
	exponent := 0
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		parsed, err := strconv.Atoi(value[i+1:])
		if err == nil {
			exponent = parsed
		}
		value = value[:i]
	}
	scale := 0
	if i := strings.IndexByte(value, '.'); i >= 0 {
		scale = len(strings.TrimRight(value[i+1:], "0"))
	}
	return max(scale-exponent, 0)
}
