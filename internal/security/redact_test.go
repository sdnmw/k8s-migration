package security

import "testing"

func TestRedactMapRecursivelyRemovesCredentials(t *testing.T) {
	input := map[string]any{
		"name": "source-a",
		"nested": map[string]any{
			"apiToken": "top-secret",
			"endpoint": "https://cluster.example",
		},
		"headers": []any{map[string]any{"Authorization": "Bearer abc"}},
		"note":    "-----BEGIN PRIVATE KEY----- data",
	}
	redacted := RedactMap(input)
	if redacted["name"] != "source-a" || redacted["note"] != Redacted {
		t.Fatalf("unexpected redaction: %#v", redacted)
	}
	nested := redacted["nested"].(map[string]any)
	if nested["apiToken"] != Redacted || nested["endpoint"] != "https://cluster.example" {
		t.Fatalf("unexpected nested redaction: %#v", nested)
	}
	header := redacted["headers"].([]any)[0].(map[string]any)
	if header["Authorization"] != Redacted {
		t.Fatalf("authorization was not redacted: %#v", header)
	}
}
