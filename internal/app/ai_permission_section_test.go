package app

import (
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
)

func TestAIPermissionApprovalSection(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
			t.Run(lang+"/"+string(mode), func(t *testing.T) { testAIPermissionApprovalSection(t, lang, mode) })
		}
	}
}

func testAIPermissionApprovalSection(t *testing.T, lang string, mode theme.Mode) {
	a, tt := newTestApp(t)
	tt.SetLocale(lang)
	tt.SetSize(1200, 3200)
	s := a.Settings()
	s.Theme = mode
	s.AIProviders = []settings.AIProvider{{ID: "review", Name: "Review test", BaseURL: "https://example.test/v1", Model: "chat-model"}}
	s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel = "review", settings.AIReviewDecisions, "custom-review"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.review.custom = true
	a.openSettings(prefAI)
	tt.Frame()
	m := locales.In(lang)
	title := "Permission approval"
	if lang == "zh-Hans" {
		title = "权限审批"
	}
	consent := mustFind(t, tt, m.AiConsentSection())
	approval := mustFind(t, tt, title)
	providers := mustFind(t, tt, m.AiProvidersSection())
	if !(consent.Y+consent.H < approval.Y && approval.Y+approval.H < providers.Y) {
		t.Fatalf("section order consent=%v approval=%v providers=%v", consent, approval, providers)
	}
	for _, label := range []string{m.AiDefaultMode(), m.AiReviewProvider(), m.AiReviewStrictness(), m.AiReviewWith(), m.AiReviewKind(), m.AiReviewModelName()} {
		row := mustFind(t, tt, label)
		if row.Y <= approval.Y+approval.H || row.Y+row.H >= providers.Y {
			t.Fatalf("%q outside approval section: %v", label, row)
		}
	}
	for _, label := range []string{m.AiDefaultAgent(), m.AiACP(), m.AiContextLines(), m.AiAuditDays(), m.AiRedact()} {
		if row := mustFind(t, tt, label); row.Y+row.H >= consent.Y {
			t.Fatalf("%q left the general section: %v", label, row)
		}
	}
	mustClick(t, tt, m.AiReviewStrict())
	tt.Frame()
	mustClick(t, tt, m.AiModeReview())
	tt.Frame()
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.AIDefaultMode != "review" || saved.AIReviewStrictness != settings.AIReviewStrict || saved.AIReviewProvider != "review" || saved.AIReviewModel != "custom-review" || saved.AIReviewKind != settings.AIReviewDecisions {
		t.Fatalf("moved controls did not preserve settings: %+v", saved)
	}
}
