package gen

import "testing"

// Pascal and Camel change the first LETTER, whatever its width: a word that starts with a multi-byte
// letter must not be cut in the middle of the letter (that is not valid UTF-8, so not an identifier).
func TestPascalCamelMultiByteFirstLetter(t *testing.T) {
	if got := Pascal("über_ok"); got != "ÜberOk" {
		t.Errorf("Pascal = %q", got)
	}
	if got := Camel("Über_ok"); got != "überOk" {
		t.Errorf("Camel = %q", got)
	}
	if got := Pascal("a_b"); got != "AB" {
		t.Errorf("Pascal = %q", got)
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{"<none>": "_none_", "it's": "it_s", "a b": "a b", "x:y": "x_y", "über": "über", "100%": "100_"} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
