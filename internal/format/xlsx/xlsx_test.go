package xlsx

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/rowbird/rowbird/internal/plugin"
	"github.com/rowbird/rowbird/internal/plugin/formattest"
)

// dump describes a workbook as text: properties, sheet, panes, filter, widths and every cell
// with its type, raw value and number format.
func dump(t *testing.T, out []byte) []byte {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("open workbook: %v", err)
	}
	defer func() { _ = f.Close() }()
	var b strings.Builder
	props, _ := f.GetDocProps()
	fmt.Fprintf(&b, "title=%q creator=%q created=%s\n", props.Title, props.Creator, props.Created)
	sheets := f.GetSheetList()
	fmt.Fprintf(&b, "sheets=%q\n", sheets)
	sheet := sheets[0]
	panes, _ := f.GetPanes(sheet)
	fmt.Fprintf(&b, "freeze=%v ysplit=%d topleft=%s\n", panes.Freeze, panes.YSplit, panes.TopLeftCell)
	for _, dn := range f.GetDefinedName() {
		fmt.Fprintf(&b, "defined %s=%s\n", dn.Name, dn.RefersTo)
	}
	rows, _ := f.GetRows(sheet, excelize.Options{RawCellValue: true})
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	for c := 1; c <= cols; c++ {
		name, _ := excelize.ColumnNumberToName(c)
		w, _ := f.GetColWidth(sheet, name)
		fmt.Fprintf(&b, "width %s=%g\n", name, w)
	}
	for r := range rows {
		for c := 1; c <= cols; c++ {
			cell, _ := excelize.CoordinatesToCellName(c, r+1)
			v, _ := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
			if v == "" {
				continue
			}
			typ, _ := f.GetCellType(sheet, cell)
			shown, _ := f.GetCellValue(sheet, cell)
			line := fmt.Sprintf("%s type=%d raw=%q", cell, typ, v)
			if shown != v {
				line += fmt.Sprintf(" shown=%q", shown)
			}
			if id, _ := f.GetCellStyle(sheet, cell); id != 0 {
				st, _ := f.GetStyle(id)
				if st.CustomNumFmt != nil {
					line += fmt.Sprintf(" fmt=%q", *st.CustomNumFmt)
				}
				if st.Font != nil && st.Font.Bold {
					line += " bold"
				}
			}
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String())
}

func TestConformance(t *testing.T) {
	formattest.Run(t, formattest.Harness{Formatter: formatter{}, Dir: "testdata/golden", Dump: dump})
}

func TestSheetName(t *testing.T) {
	cases := map[string]string{
		"Vendas por região": "Vendas por região", "a/b:c?d*e[f]": "a b c d e f", "": "Result", "  '  ": "Result",
		strings.Repeat("x", 40): strings.Repeat("x", 31),
	}
	for in, want := range cases {
		if got := SheetName(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestExactNumbers(t *testing.T) {
	in := formattest.Input("en", [][]any{{int64(123456789012345), plugin.Decimal("1234567890.12345"), plugin.Decimal("0.1")}, {int64(1234567890123456), plugin.Decimal("1234567890.123456"), plugin.Decimal("7")}},
		[]plugin.Column{{Name: "a", Type: plugin.TypeInt}, {Name: "b", Type: plugin.TypeDecimal}, {Name: "c", Type: plugin.TypeDecimal}})
	var buf bytes.Buffer
	if _, err := (formatter{}).Format(context.Background(), in, &buf); err != nil {
		t.Fatal(err)
	}
	f, _ := excelize.OpenReader(&buf)
	sheet := f.GetSheetList()[0]
	want := map[string][2]string{
		"A2": {"123456789012345", "number"}, "B2": {"1234567890.12345", "number"}, "C2": {"0.1", "number"},
		"A3": {"1234567890123456", "text"}, "B3": {"1234567890.123456", "text"}, "C3": {"7", "number"},
	}
	for cell, w := range want {
		v, _ := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
		typ, _ := f.GetCellType(sheet, cell)
		kind := "number"
		if typ == excelize.CellTypeSharedString || typ == excelize.CellTypeInlineString {
			kind = "text"
		}
		if v != w[0] || kind != w[1] {
			t.Errorf("%s = %q (%s), want %q (%s)", cell, v, kind, w[0], w[1])
		}
	}
}
