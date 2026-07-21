package i18n

import (
	"testing"
)

func resetToEnglish() {
	SetLang("en")
}

func TestT_FallbackToKey(t *testing.T) {
	t.Cleanup(resetToEnglish)
	SetLang("en")
	if got := T("nonexistent.key"); got != "nonexistent.key" {
		t.Fatalf("expected key fallback, got %q", got)
	}
}

func TestT_SpanishTranslation(t *testing.T) {
	t.Cleanup(resetToEnglish)
	SetLang("es")
	if got := T("msg.nodes_title"); got != "Nodos PVE" {
		t.Fatalf("expected Spanish translation, got %q", got)
	}
}

func TestT_FormatArgs(t *testing.T) {
	t.Cleanup(resetToEnglish)
	SetLang("en")
	if got := T("msg.nodes_saved", 2); got != "✔ 2 nodes saved" {
		t.Fatalf("expected formatted translation, got %q", got)
	}
	SetLang("es")
	if got := T("msg.nodes_saved", 2); got != "✔ 2 nodos guardados" {
		t.Fatalf("expected formatted Spanish translation, got %q", got)
	}
}

func TestDetectSystemLang(t *testing.T) {
	if got := DetectSystemLang(); got != "en" && got != "es" {
		t.Fatalf("expected en or es, got %q", got)
	}
}
