package snippet

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type PromptRole struct {
	Name     string   `json:"name" yaml:"name"`
	Role     string   `json:"role" yaml:"role"`
	Job      string   `json:"job" yaml:"job"`
	Template string   `json:"template" yaml:"template"`
	Tags     []string `json:"tags" yaml:"tags"`
}

var popularPrompts = []PromptRole{
	{
		Name: "Viral Content Creator",
		Role: "a viral content creator who has generated 100M+ impressions across Twitter/X, TikTok, and LinkedIn",
		Job:  "analyze the provided topic or draft and rewrite it into highly engaging, hook-driven content optimized for virality and high shareability. Maintain a clean, punchy format.",
		Tags: []string{"viral", "marketing", "social media", "twitter", "linkedin", "tiktok", "copywriting"},
	},
	{
		Name: "E-commerce Email Strategist",
		Role: "an e-commerce email strategist specializing in cart recovery and customer retention lifecycle marketing",
		Job:  "draft a high-converting, personalized 3-part cart abandonment email sequence that builds urgency, highlights product benefits, and addresses common purchase objections without sounding overly salesy.",
		Tags: []string{"email", "ecommerce", "marketing", "sales", "retention"},
	},
	{
		Name: "Avant-garde Writer",
		Role: "an avant-garde creative writer known for subverting traditional storytelling tropes with vivid imagery, dark humor, and stream-of-consciousness styling",
		Job:  "rewrite the following scene or text block to introduce poetic tension, non-linear timelines, or unique perspective shifts that evoke deep emotional resonance.",
		Tags: []string{"creative", "writing", "storytelling", "avant-garde", "fiction", "poetry"},
	},
	{
		Name: "Senior Software Architect",
		Role: "a Senior Software Architect and polyglot engineer with extensive experience in building highly distributed, fault-tolerant, and secure systems",
		Job:  "review the provided code or architecture proposal. Identify bottleneck risks, security vulnerabilities, and architectural smell, then provide modular, clean code recommendations with justifications.",
		Tags: []string{"code", "architecture", "software", "engineering", "review", "security"},
	},
	{
		Name: "Direct Response Copywriter",
		Role: "a world-class direct response copywriter trained in the methods of Eugene Schwartz and Gary Halbert",
		Job:  "craft a compelling sales copy or landing page section for the specified product, utilizing the AIDA (Attention, Interest, Desire, Action) framework to maximize conversion rates.",
		Tags: []string{"copywriting", "sales", "aida", "marketing", "landing page"},
	},
}

var promptDatabase = []PromptRole{
	{
		Name: "Viral Content Creator",
		Role: "a viral content creator who has generated 100M+ impressions across Twitter/X, TikTok, and LinkedIn",
		Job:  "analyze the provided topic or draft and rewrite it into highly engaging, hook-driven content optimized for virality and high shareability. Maintain a clean, punchy format.",
		Tags: []string{"viral", "marketing", "social media", "twitter", "linkedin", "tiktok", "copywriting"},
	},
	{
		Name: "E-commerce Email Strategist",
		Role: "an e-commerce email strategist specializing in cart recovery and customer retention lifecycle marketing",
		Job:  "draft a high-converting, personalized 3-part cart abandonment email sequence that builds urgency, highlights product benefits, and addresses common purchase objections without sounding overly salesy.",
		Tags: []string{"email", "ecommerce", "marketing", "sales", "retention"},
	},
	{
		Name: "Avant-garde Writer",
		Role: "an avant-garde creative writer known for subverting traditional storytelling tropes with vivid imagery, dark humor, and stream-of-consciousness styling",
		Job:  "rewrite the following scene or text block to introduce poetic tension, non-linear timelines, or unique perspective shifts that evoke deep emotional resonance.",
		Tags: []string{"creative", "writing", "storytelling", "avant-garde", "fiction", "poetry"},
	},
	{
		Name: "Senior Software Architect",
		Role: "a Senior Software Architect and polyglot engineer with extensive experience in building highly distributed, fault-tolerant, and secure systems",
		Job:  "review the provided code or architecture proposal. Identify bottleneck risks, security vulnerabilities, and architectural smell, then provide modular, clean code recommendations with justifications.",
		Tags: []string{"code", "architecture", "software", "engineering", "review", "security"},
	},
	{
		Name: "Direct Response Copywriter",
		Role: "a world-class direct response copywriter trained in the methods of Eugene Schwartz and Gary Halbert",
		Job:  "craft a compelling sales copy or landing page section for the specified product, utilizing the AIDA (Attention, Interest, Desire, Action) framework to maximize conversion rates.",
		Tags: []string{"copywriting", "sales", "aida", "marketing", "landing page"},
	},
	{
		Name: "Ruthless Editor",
		Role: "a ruthless, world-class editor with a focus on simplicity, readability, and high-impact communication",
		Job:  "edit the provided text to cut filler words, remove passive voice, simplify complex sentences, and enhance clarity while preserving the author's original voice.",
		Tags: []string{"editor", "writing", "editing", "readability", "clear"},
	},
	{
		Name: "SEO Specialist",
		Role: "an SEO Strategist and technical content optimizer who consistently ranks articles in Google's top 3 positions",
		Job:  "optimize the provided text or outline for the target keywords. Improve heading hierarchy, metadata, search intent alignment, and keyword density naturally.",
		Tags: []string{"seo", "marketing", "keywords", "search", "google"},
	},
	{
		Name: "Product Manager",
		Role: "a veteran Silicon Valley Product Manager skilled at translating vague ideas into crystal-clear product requirement documents (PRDs)",
		Job:  "draft a detailed product specification including User Persona, Success Metrics, Core Features, and Edge Case handling for the requested product idea.",
		Tags: []string{"pm", "product", "prd", "specification", "management"},
	},
	{
		Name: "UI/UX Design Critic",
		Role: "a principal Product Designer and UI/UX critic obsessed with clean layout, micro-interactions, and accessibility (WCAG)",
		Job:  "critique the provided UI layout or flow description. Suggest specific enhancements for hierarchy, layout spacing, visual cues, and user friction reduction.",
		Tags: []string{"ui", "ux", "design", "critic", "accessibility"},
	},
}

func loadPromptsDynamic() []PromptRole {
	if SnippetFilePath == "" {
		return promptDatabase
	}
	promptsPath := filepath.Join(filepath.Dir(SnippetFilePath), "prompts.yaml")
	data, err := os.ReadFile(promptsPath)
	if err != nil {
		return promptDatabase
	}
	var loaded []PromptRole
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		return promptDatabase
	}
	return loaded
}

func (s *SmartState) resolvePromptQuery() {
	database := loadPromptsDynamic()
	q := strings.ToLower(strings.TrimSpace(s.query))
	if q == "" {
		// Populate popular list dynamically or from the config database
		s.promptMatches = database
		s.promptIdx = 0
		return
	}

	var matches []PromptRole
	// 1. Exact/prefix match on name
	for _, p := range database {
		nameLower := strings.ToLower(p.Name)
		if strings.HasPrefix(nameLower, q) || strings.ReplaceAll(nameLower, " ", "") == q {
			matches = append(matches, p)
		}
	}

	// Track matches we have already found
	seen := make(map[string]bool)
	for _, m := range matches {
		seen[m.Name] = true
	}

	// 2. Substring match on name, role, or tags
	for _, p := range database {
		if seen[p.Name] {
			continue
		}
		nameLower := strings.ToLower(p.Name)
		roleLower := strings.ToLower(p.Role)
		templateLower := strings.ToLower(p.Template)
		if strings.Contains(nameLower, q) || strings.Contains(roleLower, q) || strings.Contains(templateLower, q) {
			matches = append(matches, p)
			seen[p.Name] = true
			continue
		}
		for _, tag := range p.Tags {
			if strings.HasPrefix(strings.ToLower(tag), q) || strings.ToLower(tag) == q {
				matches = append(matches, p)
				seen[p.Name] = true
				break
			}
		}
	}

	s.promptMatches = matches
	s.promptIdx = 0
}
