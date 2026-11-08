package main

import "testing"

func TestShellArg(t *testing.T) {
	t.Parallel()

	cases := []struct {
		value string
		want  string
	}{
		{"run-123", "run-123"},
		{"a/b:c_d.e+f=g", "a/b:c_d.e+f=g"},
		{"", "''"},
		{"hello world", "'hello world'"},
		{"it's", `'it'\''s'`},
		{"x;rm -rf /", "'x;rm -rf /'"},
		{"$(id)", "'$(id)'"},
		{"`id`", "'`id`'"},
		{"line\nbreak", "'line\nbreak'"},
	}
	for _, tc := range cases {
		if got := shellArg(tc.value); got != tc.want {
			t.Errorf("shellArg(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
