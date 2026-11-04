package aibrands

import (
	"bytes"
	"image"
	"image/png"
	"runtime"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
)

// artSize is the side gen.go scales the artwork to.
const artSize = 64

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
				if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != artSize || h != artSize {
					t.Fatalf("artwork is %dx%d, want %dx%d from gen.go", w, h, artSize, artSize)
				}
				bitmap := brands[name].Light()
				if theme == "dark" {
					bitmap = brands[name].Dark()
				}
				w, h := bitmap.Size()
				if w != artSize || h != artSize {
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

func TestBrandsDecodeOnUse(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	all := loadBrands()
	runtime.ReadMemStats(&after)
	// Decoding the artwork of every brand would take megabytes.
	if n := after.TotalAlloc - before.TotalAlloc; n > 256<<10 {
		t.Fatalf("loading %d brands allocated %d bytes: it decoded artwork", len(all), n)
	}
	icon := all["openai"]
	if icon.Light() != icon.Light() || icon.Dark() != icon.Dark() {
		t.Fatal("artwork decoded again for each use")
	}
	if icon.Light() == icon.Dark() {
		t.Fatal("light and dark share a bitmap")
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
