package assets

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Snippet holds metadata only; its command body belongs in the encrypted store.
type Snippet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Group     string `json:"group,omitempty"`
	SendEnter bool   `json:"sendEnter"`
}

// Normalize trims metadata without altering the separately stored command body.
func (s *Snippet) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Group = strings.TrimSpace(s.Group)
}

// Validate checks normalized metadata.
func (s Snippet) Validate() error {
	if s.Name == "" {
		return &FieldError{"snippetName", "required"}
	}
	if err := checkSnippetText("snippetName", s.Name); err != nil {
		return err
	}
	return ValidateSnippetGroup(s.Group)
}

// ValidateSnippetGroup checks a trimmed group name; "" is the default group.
func ValidateSnippetGroup(group string) error { return checkSnippetText("snippetGroup", group) }

func checkSnippetText(field, value string) error {
	if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return &FieldError{field, "invalid"}
	}
	if utf8.RuneCountInString(value) > 80 {
		return &FieldError{field, "tooLong"}
	}
	return nil
}

// ValidateSnippetContent accepts at most 64 KiB of UTF-8 command text, with
// CR, LF and TAB as the only allowed C0 controls. Errors never contain the body.
func ValidateSnippetContent(content string) error {
	reason := ""
	switch {
	case !utf8.ValidString(content):
		reason = "invalid"
	case len(content) > 64<<10:
		reason = "tooLong"
	case strings.TrimSpace(content) == "":
		reason = "required"
	case strings.ContainsFunc(content, func(r rune) bool { return r == 0x7f || r < 0x20 && r != '\r' && r != '\n' && r != '\t' }):
		reason = "invalid"
	}
	if reason != "" {
		return &FieldError{"snippetContent", reason}
	}
	return nil
}

func (s *Store) snippetIndex(id string) int {
	return slices.IndexFunc(s.snippets, func(n Snippet) bool { return n.ID == id })
}

// Snippets returns a copy sorted by group, then name, ignoring case.
func (s *Store) Snippets() []Snippet {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.snippets)
	slices.SortStableFunc(out, func(a, b Snippet) int {
		if c := strings.Compare(strings.ToLower(a.Group), strings.ToLower(b.Group)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// Snippet returns metadata for id.
func (s *Store) Snippet(id string) (Snippet, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.snippetIndex(id); i >= 0 {
		return s.snippets[i], true
	}
	return Snippet{}, false
}

// AddSnippet adds an entry under its supplied ID so its body can be saved first.
func (s *Store) AddSnippet(n Snippet) (Snippet, error) { return s.saveSnippet(n, true) }

// SaveSnippet creates an entry with an empty ID or updates an existing one.
func (s *Store) SaveSnippet(n Snippet) (Snippet, error) { return s.saveSnippet(n, false) }

func (s *Store) saveSnippet(n Snippet, add bool) (Snippet, error) {
	n.Normalize()
	if err := n.Validate(); err != nil {
		return Snippet{}, err
	}
	if n.ID == "" && !add {
		n.ID = NewID()
		add = true
	}
	if !ValidID(n.ID) {
		return Snippet{}, &FieldError{"id", "invalid"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.snippetIndex(n.ID)
	if add && i >= 0 {
		return Snippet{}, ErrExists
	}
	if !add && i < 0 {
		return Snippet{}, ErrNotFound
	}
	if err := s.commit(func() {
		if i < 0 {
			s.snippets = append(s.snippets, n)
		} else {
			s.snippets[i] = n
		}
	}); err != nil {
		return Snippet{}, err
	}
	return n, nil
}

// DeleteSnippet removes metadata; the app coordinates encrypted body deletion.
func (s *Store) DeleteSnippet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.snippetIndex(id)
	if i < 0 {
		return ErrNotFound
	}
	return s.commit(func() { s.snippets = slices.Delete(s.snippets, i, i+1) })
}
