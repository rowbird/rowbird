package slug

import "testing"

func TestMake(t *testing.T) {
	cases := map[string]string{
		"Vendas por região":        "vendas-por-regiao",
		"  Daily  sales -- 2026! ": "daily-sales-2026",
		"Ação & Reação":            "acao-reacao",
		"日本語":                      "",
	}
	for in, want := range cases {
		got := Make(in, "report")
		if want == "" {
			if len(got) != 15 || got[:7] != "report-" || !Valid(got) {
				t.Errorf("%q: got %q", in, got)
			}
			continue
		}
		if got != want || !Valid(got) {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestCheckMeta(t *testing.T) {
	if fe := CheckMeta("Sales", "sales", ""); len(fe) != 0 {
		t.Errorf("valid meta: %v", fe)
	}
	if fe := CheckMeta(" ", "Sales!", ""); len(fe) != 2 {
		t.Errorf("invalid meta: %v", fe)
	}
}
