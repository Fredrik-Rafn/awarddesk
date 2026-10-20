package eva

import (
	"awarddesk/internal/award"
	"context"
	"encoding/json"
	http "github.com/bogdanfinn/fhttp"
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestFreshMemberProfileForPricing(t *testing.T) {
	p := pageData{PageTicket: "fresh-ticket", Business: map[string]json.RawMessage{
		"TravellerList":      json.RawMessage(`{"Travellers":[{"Type":{"code":"ADT"},"IdentityInformation":{"IDEN_TitleCode":"MR","IDEN_FirstName":"TEST","IDEN_LastName":"MEMBER","IDEN_DateOfBirth":"1990/05/09","IDEN_Nationality":"TW"}}]}`),
		"ContactInformation": json.RawMessage(`{"Email":"member@example.test","PhoneMobile":"123456789","PhoneMobileCode":"886"}`),
	}}
	q := award.Query{Adults: 1}
	v, err := travellerValues(p, q)
	if err != nil {
		t.Fatal(err)
	}
	if v.Get("DATE_OF_BIRTH_1") != "199005090000" || v.Get("paxDobMonth_1") != "05" || v.Get("PAGE_TICKET") != "fresh-ticket" || v.Get("NEWS_SUBSCRIPTION") != "false" || v.Get("FIRST_NAME_1") != "TEST" {
		t.Fatal("fresh profile mapped incorrectly")
	}
	q.Adults = 2
	if _, err := travellerValues(p, q); err == nil {
		t.Fatal("missing second traveller accepted")
	}
	q.Adults = 1
	p.Business["ContactInformation"] = json.RawMessage(`{}`)
	if _, err := travellerValues(p, q); err == nil {
		t.Fatal("incomplete contact profile accepted")
	}
}

type pricingTransport func(context.Context, string, string, io.Reader, http.Header) (*http.Response, []byte, error)

func (f pricingTransport) Do(ctx context.Context, m, u string, b io.Reader, h http.Header) (*http.Response, []byte, error) {
	return f(ctx, m, u, b, h)
}

func TestCheckoutCarriesFreshStateAndProfile(t *testing.T) {
	member := pageData{PageTicket: "ticket2", Business: map[string]json.RawMessage{
		"TravellerList":      json.RawMessage(`{"Travellers":[{"Type":{"code":"ADT"},"IdentityInformation":{"IDEN_TitleCode":"MR","IDEN_FirstName":"FRESH","IDEN_LastName":"PROFILE","IDEN_DateOfBirth":"1990/05/09","IDEN_Nationality":"TW"}}]}`),
		"ContactInformation": json.RawMessage(`{"Email":"fresh@example.test","PhoneMobile":"123456789","PhoneMobileCode":"886"}`),
	}}
	count := 0
	c := pricingTransport(func(ctx context.Context, method, raw string, body io.Reader, h http.Header) (*http.Response, []byte, error) {
		count++
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		actions := []string{"AddElementsTravellerInformation", "AddElementsDynamicCatalog", "AddDynamicCatalogSelectionToTripPlan"}
		if count > 3 || method != "POST" || u.Host != "book.evaair.com" || u.Path != "/plnext/EVAOnlineDX/"+actions[count-1]+".action;jsessionid=nav"+strconv.Itoa(count) {
			t.Fatal("unexpected price-preparation endpoint")
		}
		data, _ := io.ReadAll(body)
		v, _ := url.ParseQuery(string(data))
		if v.Get("PAGE_TICKET") != "ticket"+strconv.Itoa(count) {
			t.Fatal("stale page ticket reused")
		}
		if count == 2 && (v.Get("FIRST_NAME_1") != "FRESH" || v.Get("CONTACT_POINT_EMAIL_1") != "fresh@example.test") {
			t.Fatal("live member profile not submitted")
		}
		if count == 3 && (v.Get("ACTION") != "BOOK" || v.Get("TRANSACTION_ID") != "RequestAdditionalInformation" || v.Get("CONFIRM_SERVICE_SELECTION") != "TRUE") {
			t.Fatal("wrong price-preparation action")
		}
		if count != 3 && v.Get("ACTION") != "" {
			t.Fatal("checkout action submitted early")
		}
		var response []byte
		switch count {
		case 1:
			response, _ = json.Marshal(map[string]any{"data": map[string]any{"pageDefinitionConfig": map[string]any{"pageData": member}}, "sessionId": "nav2"})
		case 2:
			response = []byte(`<script>init({config:{"siteConfiguration":{},"pageDefinitionConfig":{"pageData":{"pageTicket":"ticket3","business":{"CatalogServiceSelection":{}}}}},sessionId:"nav3"});</script>`)
		case 3:
			response = []byte(`{"data":{"pageTicket":"ticket4","business":{"MilesAndCashView":{"maxRankAvailable":{"milesAmount":16800,"nonConvertedAmount":{"amount":3040}}}}},"sessionId":"nav4"}`)
		}
		return &http.Response{StatusCode: 200}, response, nil
	})
	p, err := checkoutQuote(context.Background(), c, pageData{SessionID: "nav1", PageTicket: "ticket1"}, award.Query{Adults: 1, Cabin: award.Economy})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || p.PageTicket != "ticket4" || p.SessionID != "nav4" || !strings.Contains(string(p.Business["MilesAndCashView"]), "16800") {
		t.Fatal("final price or state lost")
	}
}

func TestNavigationAJAXEnvelope(t *testing.T) {
	p, err := decodePage([]byte(`{"data":{"pageDefinitionConfig":{"pageData":{"pageTicket":"next-ticket","business":{"Price":{}}}}},"sessionId":"navigation-session"}`))
	if err != nil || p.PageTicket != "next-ticket" || p.SessionID != "navigation-session" || p.Business["Price"] == nil {
		t.Fatal("navigation AJAX response lost pricing state")
	}
}
