package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrdinaryWebhookBindingValidation(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		valid       bool
	}{
		{"valid", `"endpoint":"https://receiver.example/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1","validitySeconds":600,"credential":{"kind":"env","reference":"WEBHOOK_KEY"}`, true},
		{"missing credential", `"endpoint":"https://receiver.example/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1","validitySeconds":600`, false},
		{"wrong profile", `"endpoint":"https://receiver.example/api/v1/webhookreceiver/webhook/other","validitySeconds":600,"credential":{"kind":"env","reference":"WEBHOOK_KEY"}`, false},
		{"missing validity", `"endpoint":"https://receiver.example/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1","credential":{"kind":"env","reference":"WEBHOOK_KEY"}`, false},
		{"secret query", `"endpoint":"https://receiver.example/api/v1/webhookreceiver/webhook/corrtest_ordinary_v1?secret=bad","validitySeconds":600,"credential":{"kind":"env","reference":"WEBHOOK_KEY"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input TargetInput
			if err := json.Unmarshal([]byte(`{"displayName":"lab","baseUrl":"https://public.example","ordinaryWebhook":{`+tc.extra+`}}`), &input); err != nil {
				t.Fatal(err)
			}
			err := input.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if tc.valid {
				raw, _ := json.Marshal(input)
				if !strings.Contains(string(raw), "receiver.example") {
					t.Fatal("receiver binding discarded")
				}
			}
		})
	}
}
