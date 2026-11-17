package config

import "testing"

// Pins topic validation: ASCII word characters and hyphens only, fully anchored.
func TestNotificationTopicPatternPinned(t *testing.T) {
	for topic, valid := range map[string]bool{
		"quota-alerts_01": true,
		"Q":               true,
		"quota alerts":    false,
		"quota\n":         false,
		"quota.alerts":    false,
		"quota/alerts":    false,
		"ümlaut":          false,
		"quota\u0130":     false,
		"\n":              false,
	} {
		config := NotificationsConfig{WarningPercent: 20, CriticalPercent: 5, Topic: topic}
		if got := config.Validate() == nil; got != valid {
			t.Errorf("Validate(topic %q) valid = %v, want %v", topic, got, valid)
		}
	}
}
