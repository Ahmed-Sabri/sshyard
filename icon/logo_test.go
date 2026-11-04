package icon

import "testing"

func TestBitmapIsUISized(t *testing.T) {
	a, b := Bitmap(), Bitmap()
	if a == b {
		t.Fatal("Bitmap returned the same bitmap twice")
	}
	if w, h := a.Size(); w != uiSize || h != uiSize {
		t.Fatalf("Bitmap size = %dx%d, want %dx%d", w, h, uiSize, uiSize)
	}
}
