package helps

import (
	"strings"
	"testing"
)

// Pins strict account-info JSON decoding, including payloads larger than a
// streaming decoder window, trailing data and json.Number preservation.
func TestDecodeAccountInfoObjectPinned(t *testing.T) {
	large := strings.Repeat("x", 600*1024)
	for _, tc := range []struct {
		name    string
		payload string
		ok      bool
	}{
		{"object", `{"a":1}`, true},
		{"surrounding whitespace", " \n\t{\"a\":1} \r\n", true},
		{"trailing object", `{"a":1}{"b":2}`, false},
		{"trailing garbage", `{"a":1} x`, false},
		{"trailing comma", `{"a":1},`, false},
		{"null", `null`, false},
		{"array", `[1]`, false},
		{"string", `"x"`, false},
		{"empty", ``, false},
		{"truncated", `{"a":`, false},
		{"invalid UTF-8", "{\"a\":\"\xff\"}", true},
		{"BOM", "\xef\xbb\xbf{\"a\":1}", false},
		{"duplicate keys", `{"a":1,"a":2}`, true},
		{"large value", `{"a":"` + large + `"}`, true},
		{"large value then object", `{"a":"` + large + `"}{}`, false},
		{"large whitespace then garbage", `{"a":1}` + strings.Repeat(" ", 600*1024) + `x`, false},
	} {
		object, err := decodeAccountInfoObject([]byte(tc.payload))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok %v", tc.name, err, tc.ok)
			continue
		}
		if tc.name == "large value" && object["a"] != large {
			t.Errorf("large value was not preserved")
		}
	}

	object, err := decodeAccountInfoObject([]byte(`{"big":12345678901234567890,"int":42,"float":1.50,"exp":1e2,"neg":-0,"dup":1,"dup":2,"bad":"\ud800","nested":{"n":7}}`))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := accountInfoInteger(object["big"], 0); ok {
		t.Errorf("integer above int64 accepted: %v", v)
	}
	if v, ok := accountInfoInteger(object["int"], 0); !ok || v != 42 {
		t.Errorf("int = %v, %v", v, ok)
	}
	if _, ok := accountInfoInteger(object["float"], 0); ok {
		t.Errorf("1.50 accepted as an integer")
	}
	if v, ok := accountInfoPercentage(object["float"]); !ok || v != 1.5 {
		t.Errorf("float percentage = %v, %v", v, ok)
	}
	if _, ok := accountInfoInteger(object["exp"], 0); ok {
		t.Errorf("1e2 accepted as an integer")
	}
	if v, ok := accountInfoPercentage(object["exp"]); !ok || v != 100 {
		t.Errorf("1e2 percentage = %v, %v", v, ok)
	}
	if v, ok := accountInfoInteger(object["neg"], 0); !ok || v != 0 {
		t.Errorf("-0 = %v, %v", v, ok)
	}
	for key, want := range map[string]string{"big": "12345678901234567890", "float": "1.50", "exp": "1e2", "neg": "-0", "dup": "2"} {
		if got, _ := object[key].(interface{ String() string }); got == nil || got.String() != want {
			t.Errorf("%s = %#v, want Number %s", key, object[key], want)
		}
	}
	if object["bad"] != "\ufffd" {
		t.Errorf("lone surrogate = %q", object["bad"])
	}
	if nested, ok := object["nested"].(map[string]any); !ok || nested["n"] == nil {
		t.Errorf("nested = %#v", object["nested"])
	}
}
