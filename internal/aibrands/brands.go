// Package aibrands embeds LobeHub's original AI brand artwork, without tinting.
package aibrands

import (
	"embed"
	"fmt"
	"net/url"
	"strings"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/ui"
)

//go:embed png/light/*.png png/dark/*.png
var files embed.FS

// Icon contains the upstream artwork for each application theme.
type Icon struct {
	Name        string
	Light, Dark *ui.Bitmap
}

var brands = loadBrands()

func loadBrands() map[string]*Icon {
	entries, err := files.ReadDir("png/light")
	if err != nil {
		panic(err)
	}
	all := make(map[string]*Icon, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".png"), "-color")
		all[name] = &Icon{Name: name, Light: decode("light", entry.Name()), Dark: decode("dark", entry.Name())}
	}
	return all
}

func decode(theme, name string) *ui.Bitmap {
	path := "png/" + theme + "/" + name
	data, err := files.ReadFile(path)
	if err != nil {
		panic(err)
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		panic(fmt.Errorf("AI brand %s: %w", path, err))
	}
	return bitmap
}

var providerDomains = []struct{ domain, brand string }{
	{"openai.azure.com", "azure"}, {"services.ai.azure.com", "azure"},
	{"openai.com", "openai"}, {"anthropic.com", "anthropic"},
	{"googleapis.com", "google"}, {"google.com", "google"},
	{"deepseek.com", "deepseek"}, {"dashscope.aliyuncs.com", "qwen"},
	{"dashscope-intl.aliyuncs.com", "qwen"}, {"aliyuncs.com", "qwen"},
	{"x.ai", "grok"}, {"mistral.ai", "mistral"},
	{"moonshot.cn", "moonshot"}, {"moonshot.ai", "moonshot"},
	{"bigmodel.cn", "zhipu"}, {"z.ai", "zhipu"},
	{"minimax.chat", "minimax"}, {"minimax.io", "minimax"}, {"minimaxi.com", "minimax"},
	{"volces.com", "volcengine"}, {"volcengine.com", "volcengine"},
	{"openrouter.ai", "openrouter"}, {"ollama.com", "ollama"},
	{"siliconflow.cn", "siliconcloud"}, {"siliconflow.com", "siliconcloud"},
	{"groq.com", "groq"}, {"cohere.com", "cohere"}, {"cohere.ai", "cohere"},
	{"perplexity.ai", "perplexity"}, {"together.xyz", "together"}, {"together.ai", "together"},
	{"typesafe.ai", "typesafeai"},
}

var providerNames = []struct{ brand, aliases string }{
	{"typesafeai", "typesafe|type safe"}, {"openrouter", "openrouter|open router"},
	{"siliconcloud", "siliconflow|siliconcloud|硅基流动"}, {"azure", "azure"},
	{"anthropic", "anthropic"}, {"claude", "claude"}, {"deepseek", "deepseek|深度求索"},
	{"gemini", "gemini"}, {"google", "google|谷歌"}, {"qwen", "qwen|dashscope|千问|通义|百炼"},
	{"grok", "grok|xai|x.ai"}, {"mistral", "mistral"}, {"kimi", "kimi"},
	{"moonshot", "moonshot|月之暗面"}, {"zhipu", "zhipu|bigmodel|z.ai|智谱"},
	{"minimax", "minimax"}, {"doubao", "doubao|豆包"}, {"volcengine", "volcengine|火山"},
	{"ollama", "ollama"}, {"groq", "groq"}, {"cohere", "cohere"},
	{"perplexity", "perplexity"}, {"together", "together"}, {"meta", "meta|llama"},
	{"openai", "openai|open ai"},
}

// Provider uses the endpoint's actual hostname, then the display name, then
// the wire format's brand when a custom gateway has no known identity.
func Provider(p settings.AIProvider) *Icon {
	if endpoint, err := url.Parse(p.BaseURL); err == nil {
		host := strings.ToLower(endpoint.Hostname())
		for _, candidate := range providerDomains {
			if host == candidate.domain || strings.HasSuffix(host, "."+candidate.domain) {
				return brands[candidate.brand]
			}
		}
	}
	name := strings.ToLower(strings.TrimSpace(p.Name))
	for _, candidate := range providerNames {
		for _, alias := range strings.Split(candidate.aliases, "|") {
			if strings.Contains(name, alias) {
				return brands[candidate.brand]
			}
		}
	}
	switch p.Format {
	case settings.AIFormatAnthropic:
		return brands["anthropic"]
	case settings.AIFormatTypeSafe:
		return brands["typesafeai"]
	default:
		return brands["openai"]
	}
}

var modelFamilies = []struct{ brand, prefixes string }{
	{"openai", "gpt|chatgpt|o1|o3|o4|o5|codex"},
	{"claude", "claude"}, {"gemini", "gemini"}, {"gemma", "gemma"},
	{"deepseek", "deepseek"}, {"qwen", "qwen|qwq|qvq"}, {"grok", "grok"},
	{"meta", "llama|meta-llama"}, {"mistral", "mistral|mixtral|codestral|magistral|devstral|ministral|pixtral"},
	{"kimi", "kimi|moonshot"}, {"chatglm", "glm|chatglm"}, {"minimax", "minimax"},
	{"doubao", "doubao"}, {"cohere", "command-r|command-a"}, {"perplexity", "sonar"},
}

// Model selects the model's brand before the provider, without changing the
// original model ID used in API requests; organization prefixes are ignored.
func Model(model string, p settings.AIProvider) *Icon {
	name := strings.ToLower(strings.TrimSpace(model))
	name = name[strings.LastIndex(name, "/")+1:]
	for _, candidate := range modelFamilies {
		for _, prefix := range strings.Split(candidate.prefixes, "|") {
			if strings.HasPrefix(name, prefix) {
				return brands[candidate.brand]
			}
		}
	}
	return Provider(p)
}
