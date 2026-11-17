package codex

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// Pins the device-code exchange JSON: request bytes, key case and interval forms.
func TestDeviceAuthStartJSONPinned(t *testing.T) {
	for _, tc := range []struct {
		body     string
		code     string
		interval time.Duration
		expires  time.Duration
	}{
		{`{"device_auth_id":"id","user_code":"A","interval":7,"expires_in":"60"}`, "A", 7 * time.Second, 60 * time.Second},
		{`{"DEVICE_AUTH_ID":"id","User_Code":" B ","interval":" 7 ","expires_in":60}`, "B", 7 * time.Second, 60 * time.Second},
		{`{"device_auth_id":"id","user_code":"","usercode":"C","interval":"\u0037"}`, "C", 7 * time.Second, DeviceAuthLifetime},
		{`{"device_auth_id":"id","user_code":"D","interval":7.0,"expires_in":1e2}`, "D", 5 * time.Second, DeviceAuthLifetime},
		{`{"device_auth_id":"id","user_code":"E","extra":{"nested":[1,2]}} `, "E", 5 * time.Second, DeviceAuthLifetime},
	} {
		now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
		d := NewDeviceAuth(nil)
		d.now = func() time.Time { return now }
		var sent string
		d.auth.httpClient.Transport = deviceRoundTrip(func(r *http.Request) (*http.Response, error) {
			data, _ := io.ReadAll(r.Body)
			sent = string(data)
			return deviceResponse(200, tc.body), nil
		})
		challenge, err := d.Start(context.Background())
		if err != nil {
			t.Fatalf("Start(%s): %v", tc.body, err)
		}
		if sent != `{"client_id":"`+ClientID+`"}` {
			t.Fatalf("request body = %q", sent)
		}
		if challenge.UserCode != tc.code || challenge.Interval != tc.interval || !challenge.ExpiresAt.Equal(now.Add(tc.expires)) {
			t.Fatalf("Start(%s) = code %q interval %s expires %s", tc.body, challenge.UserCode, challenge.Interval, challenge.ExpiresAt.Sub(now))
		}
	}
}
