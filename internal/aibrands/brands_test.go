package aibrands

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
)

func TestArtwork(t *testing.T) {
	entries, err := files.ReadDir("png/light")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			for _, theme := range []string{"light", "dark"} {
				data, err := files.ReadFile("png/" + theme + "/" + entry.Name())
				if err != nil {
					t.Fatal(err)
				}
				img, err := png.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".png"), "-color")
				bitmap := brands[name].Light
				if theme == "dark" {
					bitmap = brands[name].Dark
				}
				w, h := bitmap.Size()
				if w <= 0 || h <= 0 || w != img.Bounds().Dx() || h != img.Bounds().Dy() {
					t.Fatalf("invalid decoded bitmap %dx%d", w, h)
				}
				if strings.Contains(entry.Name(), "-color") && !hasColor(img) {
					t.Fatal("color artwork lost its original colors")
				}
			}
		})
	}
	light, _ := files.ReadFile("png/light/openai.png")
	dark, _ := files.ReadFile("png/dark/openai.png")
	if bytes.Equal(light, dark) {
		t.Fatal("monochrome OpenAI must have separate theme artwork")
	}
}

func hasColor(img image.Image) bool {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 0 && (r != g || g != b) {
				return true
			}
		}
	}
	return false
}

func TestModelBrandsAndProviderFallback(t *testing.T) {
	p := settings.AIProvider{Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1"}
	cases := map[string]string{
		"OpenAI/GPT-4.1": "openai", "o3-mini": "openai", "O4-mini": "openai",
		"Anthropic/Claude-Sonnet-4": "claude", "google/gemini-2.5-pro": "gemini",
		"google/gemma-3-27b-it":   "gemma",
		"deepseek-ai/DeepSeek-R1": "deepseek", "Qwen/Qwen3-32B": "qwen", "x-ai/grok-4": "grok",
		"meta-llama/Llama-4-Scout": "meta", "mistralai/Mistral-Large": "mistral",
		"moonshotai/Kimi-K2": "kimi", "z-ai/GLM-4.5": "chatglm", "MiniMax/MiniMax-M2": "minimax",
		"Doubao-Seed-1.6": "doubao", "unknown-model": "openrouter", "": "openrouter",
		"qwen/unknown-model": "openrouter",
	}
	for model, want := range cases {
		if got := Model(model, p).Name; got != want {
			t.Errorf("Model(%q) = %s, want %s", model, got, want)
		}
	}
	if p.Name != "OpenRouter" || p.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatal("matching mutated provider")
	}
}

func TestProviderBrands(t *testing.T) {
	cases := []struct {
		p    settings.AIProvider
		want string
	}{
		{settings.AIProvider{Name: "Gateway", BaseURL: "https://api.deepseek.com/v1"}, "deepseek"},
		{settings.AIProvider{Name: "OpenAI", BaseURL: "https://api.anthropic.com/v1"}, "anthropic"},
		{settings.AIProvider{BaseURL: "https://my.openai.azure.com/openai"}, "azure"},
		{settings.AIProvider{BaseURL: "https://generativelanguage.googleapis.com/v1beta"}, "google"},
		{settings.AIProvider{BaseURL: "https://api.deepseek.com.example.org/v1"}, "openai"},
		{settings.AIProvider{BaseURL: "https://example.org/deepseek.com"}, "openai"},
		{settings.AIProvider{Name: "硅基流动"}, "siliconcloud"},
		{settings.AIProvider{Name: "TypeSafe AI"}, "typesafeai"},
		{settings.AIProvider{Format: settings.AIFormatTypeSafe}, "typesafeai"},
		{settings.AIProvider{Format: settings.AIFormatAnthropic}, "anthropic"},
		{settings.AIProvider{Format: settings.AIFormatResponses}, "openai"},
	}
	for _, tc := range cases {
		if got := Provider(tc.p).Name; got != tc.want {
			t.Errorf("Provider(%+v) = %s, want %s", tc.p, got, tc.want)
		}
	}
}
