package provider

import (
	"context"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func easyPaySecurityTestProvider() *EasyPay {
	return &EasyPay{config: map[string]string{
		"pid":       "1000",
		"pkey":      "MERCHANT_SECRET_KEY",
		"apiBase":   "https://pay.example.com",
		"notifyUrl": "https://site.example.com/api/v1/payment/webhook/easypay",
		"returnUrl": "https://site.example.com/payment/result",
	}}
}

func TestEasyPayNotifyRejectsForgedSignReuseCallback(t *testing.T) {
	t.Parallel()
	e := easyPaySecurityTestProvider()

	returnURL := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success&trade_status=TRADE_SUCCESS"
	createParams := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "ORDER123",
		"notify_url":   e.config["notifyUrl"],
		"return_url":   returnURL,
		"name":         "balance recharge",
		"money":        "650.00",
	}
	sign := easyPaySign(createParams, e.config["pkey"])

	prefix := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success"
	callback := url.Values{}
	callback.Set("pid", "1000")
	callback.Set("type", "alipay")
	callback.Set("out_trade_no", "ORDER123")
	callback.Set("notify_url", e.config["notifyUrl"])
	callback.Set("name", "balance recharge")
	callback.Set("money", "650.00")
	callback.Set("return_url", prefix)
	rawCallback := callback.Encode() + "&trade_status=TRADE_SUCCESS&sign=" + sign + "&sign_type=MD5"

	if _, err := e.VerifyNotification(context.Background(), rawCallback, nil); err == nil {
		t.Fatal("forged sign-reuse callback must be rejected")
	}
}

func TestEasyPayNotifyRejectsOrderURLReplay(t *testing.T) {
	t.Parallel()
	e := easyPaySecurityTestProvider()

	params := map[string]string{
		"pid":          "1000",
		"type":         "alipay",
		"out_trade_no": "ORDER123",
		"notify_url":   e.config["notifyUrl"],
		"return_url":   "https://site.example.com/payment/result",
		"name":         "balance recharge",
		"money":        "650.00",
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}

	if _, err := e.VerifyNotification(context.Background(), query.Encode(), nil); err == nil {
		t.Fatal("replayed order URL must be rejected")
	}
}

func TestEasyPayNotifyAcceptsGenuineCallback(t *testing.T) {
	t.Parallel()
	e := easyPaySecurityTestProvider()

	params := map[string]string{
		"pid":          "1000",
		"trade_no":     "2026100622001400000001",
		"out_trade_no": "ORDER123",
		"type":         "alipay",
		"name":         "balance recharge",
		"money":        "650.00",
		"trade_status": tradeStatusSuccess,
		"param":        "xiass-recharge",
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}

	notification, err := e.VerifyNotification(context.Background(), query.Encode(), nil)
	if err != nil {
		t.Fatalf("genuine callback rejected: %v", err)
	}
	if notification.Status != payment.ProviderStatusSuccess {
		t.Fatalf("status = %v, want success", notification.Status)
	}
	if notification.OrderID != "ORDER123" || notification.TradeNo != "2026100622001400000001" || notification.Amount != 650.00 {
		t.Fatalf("unexpected notification: %+v", notification)
	}
}

func TestEasyPayNotifyRejectsUnknownParamEvenWhenEmpty(t *testing.T) {
	t.Parallel()
	e := easyPaySecurityTestProvider()

	params := map[string]string{
		"pid":          "1000",
		"trade_no":     "T1",
		"out_trade_no": "ORDER123",
		"type":         "alipay",
		"name":         "balance recharge",
		"money":        "650.00",
		"trade_status": tradeStatusSuccess,
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}
	query.Set("device", "")

	if _, err := e.VerifyNotification(context.Background(), query.Encode(), nil); err == nil {
		t.Fatal("callback with unknown empty param must be rejected")
	}
}
