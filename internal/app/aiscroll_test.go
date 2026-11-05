package app

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func aiScrollPane(t *testing.T, dark bool) (*ui.Tester, *conn) {
	t.Helper()
	a, tt := newTestApp(t)
	tt.SetDark(dark)
	h := savedHost(t, a, "web", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cn := a.conns[id]
	for i := range 40 {
		cn.ai.items = append(cn.ai.items, aiItem{kind: "user", text: fmt.Sprintf("Question %d", i)})
	}
	// Sending a question and opening history both request the conversation's end.
	cn.ai.scroll.Y = math.MaxFloat32
	tt.Frame()
	tt.Frame()
	if st := &cn.ai.scroll; st.MaxY <= 0 || st.Y != st.MaxY {
		t.Fatalf("timeline not at bottom: %+v", st)
	}
	return tt, cn
}

// A mouse wheel must leave the bottom of a conversation, just as a touchpad does.
func TestAITimelineScrollFromBottom(t *testing.T) {
	for _, tc := range []struct {
		dark, wheel bool
		dy          float32
	}{
		{dy: -60}, {dy: -1},
		{wheel: true, dy: -60}, {wheel: true, dy: -1},
		{dark: true, dy: -60}, {dark: true, dy: -1},
		{dark: true, wheel: true, dy: -60}, {dark: true, wheel: true, dy: -1},
	} {
		t.Run(fmt.Sprintf("dark=%v/wheel=%v/dy=%v", tc.dark, tc.wheel, tc.dy), func(t *testing.T) {
			tt, cn := aiScrollPane(t, tc.dark)
			st := &cn.ai.scroll
			r := mustFind(t, tt, "Question 39")
			before := st.Y
			if tc.wheel {
				tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, tc.dy)
			} else {
				tt.Scroll(r.X+r.W/2, r.Y+r.H/2, 0, tc.dy)
			}
			tt.Advance(300 * time.Millisecond)
			tt.Frame()
			if st.Y > before+tc.dy/2 {
				t.Fatalf("upward scroll stayed at bottom: before=%v after=%v max=%v", before, st.Y, st.MaxY)
			}
		})
	}
}

func TestAITimelineFollowsReply(t *testing.T) {
	for _, tc := range []struct{ dark, streaming bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		t.Run(fmt.Sprintf("dark=%v/streaming=%v", tc.dark, tc.streaming), func(t *testing.T) {
			tt, cn := aiScrollPane(t, tc.dark)
			cn.ai.running = tc.streaming
			t.Cleanup(func() { cn.ai.running = false })
			st := &cn.ai.scroll
			cn.ai.items = append(cn.ai.items, aiItem{kind: "text", text: "Reply begins"})
			tt.Frame()
			tt.Advance(600 * time.Millisecond)
			if st.Y != st.MaxY {
				t.Fatalf("new reply not followed: %+v", st)
			}
			r := mustFind(t, tt, "Reply begins")
			x, y := r.X+r.W/2, r.Y+r.H/2
			tt.Wheel(x, y, 0, -1)
			tt.Advance(300 * time.Millisecond)
			before, oldMax := st.Y, st.MaxY
			if before >= oldMax {
				t.Fatalf("wheel did not leave bottom: %+v", st)
			}
			cn.ai.items[len(cn.ai.items)-1].text += strings.Repeat("\n\nMore reply", 10)
			tt.Frame()
			tt.Advance(600 * time.Millisecond)
			if st.MaxY <= oldMax || st.Y != before {
				t.Fatalf("growing reply moved history: before=%v oldMax=%v after=%+v", before, oldMax, st)
			}
			tt.Wheel(x, y, 0, 10000)
			tt.Advance(300 * time.Millisecond)
			if st.Y != st.MaxY {
				t.Fatalf("downward wheel did not return to bottom: %+v", st)
			}
			cn.ai.items[len(cn.ai.items)-1].text += "\n\nReply ends"
			tt.Frame()
			tt.Advance(600 * time.Millisecond)
			if st.Y != st.MaxY {
				t.Fatalf("following did not resume at bottom: %+v", st)
			}
		})
	}
}
