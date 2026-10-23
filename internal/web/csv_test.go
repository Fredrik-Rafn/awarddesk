package web

import (
	"strings"
	"testing"
)

func TestCSVCellsCannotExecuteFormula(t *testing.T) {
	for _, v := range []string{"=SUM(A1:A2)", " +1+1", "-42", "@example", "\t=1"} {
		out := csvCell(v)
		if !strings.HasPrefix(out, "'") {
			t.Fatalf("unsafe spreadsheet cell %q", v)
		}
	}
	if csvCell("   ") != "   " || csvCell("TPE") != "TPE" {
		t.Fatal("ordinary values changed")
	}
}
