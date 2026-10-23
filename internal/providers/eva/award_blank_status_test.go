package eva

import (
	"fmt"
	"strings"
	"testing"

	"awarddesk/internal/award"
)

func awardBlankStatusFixture(statusImage string) (award.Query, []byte) {
	q := award.Query{Provider: "eva", Origin: "TPE", Destination: "HKG", Date: "2026-11-27", Cabin: award.Economy, Adults: 1, Mode: "award"}
	var body strings.Builder
	body.WriteString(`<form action="award-upgrade-availability.aspx"><input name="ctl00$content$hid_Dep" value="TPE"><input name="ctl00$content$hid_Arr" value="HKG"><input name="ctl00$content$ddl_PassengerCount" value="1"><input name="ctl00$content$rbn_SearchType" value="award"><input name="ctl00$content$ddl_Award_Cabin" value="EY"><input name="ctl00$content$rbn_Segment" value="ONE_WAY"><input name="ctl00$content$txt_tbGo2YYYYMM" value="2026-11-27"><div id="mainContent"><div id="content_control_Award_AvailabilityGO_div_Result"><table>`)
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&body, `<tr><th class="table-flightNumber">BR%d</th><td class="table-day" aria-label="27 Nov.2026Friday"><img src="BR.png" alt="Available"></td></tr>`, i)
	}
	body.WriteString(`<tr><th class="table-flightNumber">BR6</th><td class="table-day" aria-label="27 Nov.2026Friday"><img src="Triangle.png" alt="Ｗaiting"></td></tr>`)
	fmt.Fprintf(&body, `<tr><th class="table-flightNumber">BR869</th><td class="table-day" aria-label="26 Nov.2026Thursday"><img src="BR.png" alt="Available"></td><td class="table-day" aria-label="27 Nov.2026Friday">%s</td></tr>`, statusImage)
	body.WriteString(`</table></div></div></form>`)
	return q, []byte(body.String())
}

func TestAwardAvailabilityBlankStatusRetainsOtherAvailableFlights(t *testing.T) {
	q, body := awardBlankStatusFixture(`<img id="content_control_Award_AvailabilityGO_lv_Schedule_lv_DetailDate_3_img_Status_3" class="icon icon-flightCompany table-flightCompany" />`)
	available, err := parseAwardAvailability(body, q)
	if err != nil || len(available) != 5 || available["BR869"] || available["BR6"] {
		t.Fatalf("blank date status aborted search or became available: flights=%v err=%v", available, err)
	}
	for i := 1; i <= 5; i++ {
		if !available[fmt.Sprintf("BR%d", i)] {
			t.Fatalf("known available flight BR%d was lost", i)
		}
	}
}

func TestAwardAvailabilityOnlyRecognizesExactBlankStatusPlaceholder(t *testing.T) {
	for name, image := range map[string]string{
		"unknown labelled status": `<img class="icon icon-flightCompany table-flightCompany" alt="Unknown status">`,
		"unlabelled image":        `<img class="icon icon-flightCompany table-flightCompany" src="new-status.png">`,
		"wrong image class":       `<img class="other-placeholder">`,
		"missing image":           ``,
		"unexpected status text":  `<img class="icon icon-flightCompany table-flightCompany">Unexpected status`,
	} {
		t.Run(name, func(t *testing.T) {
			q, body := awardBlankStatusFixture(image)
			if _, err := parseAwardAvailability(body, q); err == nil {
				t.Fatal("unfamiliar status was silently treated as unavailable")
			}
		})
	}
}
