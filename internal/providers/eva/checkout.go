package eva

import (
	"awarddesk/internal/award"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// The primary traveller must be the authenticated member. Other adult slots use
// synthetic profiles for this aggregation-only quotation flow. These are never
// ticketed; no payment details or ticketing endpoints are used.
func travellerValues(page pageData, q award.Query) (url.Values, error) {
	var list struct {
		Travellers []struct {
			Type                struct{ Code string }
			IdentityInformation struct {
				Title       string `json:"IDEN_TitleCode"`
				First       string `json:"IDEN_FirstName"`
				Last        string `json:"IDEN_LastName"`
				DOB         string `json:"IDEN_DateOfBirth"`
				Nationality string `json:"IDEN_Nationality"`
			}
		}
	}
	var contact struct{ Email, PhoneHome, PhoneHomeCode, PhoneMobile, PhoneMobileCode string }
	if json.Unmarshal(page.Business["TravellerList"], &list) != nil || json.Unmarshal(page.Business["ContactInformation"], &contact) != nil || len(list.Travellers) != q.Adults {
		return nil, award.Failure("profile_required", "EVA did not return a complete traveller profile for this search")
	}
	v := ajaxContext(page)
	v.Set("TYPE", "AIR_TRIP_FARE")
	v.Set("FROM_PAX", "TRUE")
	for i, t := range list.Travellers {
		p := t.IdentityInformation
		if i > 0 && t.Type.Code == "ADT" {
			if p.Title == "" {
				p.Title = "MR"
			}
			if p.First == "" {
				p.First = "Guest" + string(rune('A'+i))
			}
			if p.Last == "" {
				p.Last = "Traveller"
			}
			if p.Nationality == "" {
				p.Nationality = list.Travellers[0].IdentityInformation.Nationality
			}
			if p.DOB == "" {
				p.DOB = "1990/01/" + fmt.Sprintf("%02d", i+1)
			}
		}
		if t.Type.Code != "ADT" || p.Title == "" || p.First == "" || p.Last == "" || p.Nationality == "" {
			return nil, award.Failure("profile_required", "EVA traveller profile requires name, title and nationality")
		}
		dob, err := time.Parse("2006/01/02", p.DOB)
		if err != nil {
			return nil, award.Failure("profile_required", "EVA traveller profile birth date is missing or unsupported")
		}
		n := strconv.Itoa(i + 1)
		for k, value := range map[string]string{"TRAVELLER_TYPE_": "ADT", "TITLE_": p.Title, "FIRST_NAME_": p.First, "LAST_NAME_": p.Last, "NATIONALITY_": p.Nationality, "DATE_OF_BIRTH_": dob.Format("20060102") + "0000", "paxDobDay_": dob.Format("02"), "paxDobMonth_": dob.Format("01"), "paxDobYear_": dob.Format("2006")} {
			v.Set(k+n, value)
		}
	}
	if contact.Email == "" || contact.PhoneMobile == "" || contact.PhoneMobileCode == "" {
		return nil, award.Failure("profile_required", "EVA member contact email and mobile number are required for pricing")
	}
	for k, value := range map[string]string{"CONTACT_POINT_EMAIL_1": contact.Email, "CONTACT_POINT_HOME_PHONE": contact.PhoneHome, "CONTACT_POINT_HOME_PHONE_OC": contact.PhoneHomeCode, "CONTACT_POINT_MOBILE": contact.PhoneMobile, "CONTACT_POINT_MOBILE_1_OC": contact.PhoneMobileCode, "NEWS_SUBSCRIPTION": "false", "gotoServices": "on", "SKIP_INSURANCE_CALL": "TRUE"} {
		v.Set(k, value)
	}
	return v, nil
}

func advancePage(previous, next pageData) (pageData, error) {
	if next.PageTicket == "" {
		return next, award.Failure("pricing_protocol", "EVA omitted the next pricing page state")
	}
	if next.SessionID == "" {
		next.SessionID = previous.SessionID
	}
	merged := map[string]string{}
	for k, v := range previous.Basefacts {
		merged[k] = v
	}
	for k, v := range next.Basefacts {
		merged[k] = v
	}
	next.Basefacts = merged
	return next, nil
}

// ACTION=BOOK is the user-authorized price-preparation step captured in the
// HAR. This adapter never sends payment details or calls ticketing endpoints.
func checkoutQuote(ctx context.Context, c pricingClient, page pageData, q award.Query) (pageData, error) {
	v := ajaxContext(page)
	v.Set("FROM_PAX", "false")
	v.Set("TYPE", "AIR_TRIP_FARE")
	v.Set("CABIN", map[award.Cabin]string{award.Economy: "M", award.PremiumEconomy: "W", award.Business: "C"}[q.Cabin])
	next, err := quoteRequest(ctx, c, page.SessionID, "travellers", v)
	if err != nil {
		return page, err
	}
	page, err = advancePage(page, next)
	if err != nil {
		return page, err
	}
	v, err = travellerValues(page, q)
	if err != nil {
		return page, err
	}
	next, err = quoteRequest(ctx, c, page.SessionID, "catalog", v)
	if err != nil {
		return page, err
	}
	page, err = advancePage(page, next)
	if err != nil {
		return page, err
	}
	v = ajaxContext(page)
	v.Set("TRANSACTION_ID", "RequestAdditionalInformation")
	v.Set("CONFIRM_SERVICE_SELECTION", "TRUE")
	v.Set("ACTION", "BOOK")
	next, err = quoteRequest(ctx, c, page.SessionID, "checkout price", v)
	if err != nil {
		return page, err
	}
	return advancePage(page, next)
}
