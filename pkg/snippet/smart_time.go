package snippet

import (
	_ "time/tzdata"

	"strings"
	"time"
)

// presetLocations is the built-in cycle list (shown with Left/Right arrows).
// Index 0 is always "local", which uses time.Local.
var presetLocations = []struct {
	label string
	iana  string
}{
	{"Local", ""},
	{"London", "Europe/London"},
	{"Paris", "Europe/Paris"},
	{"Tokyo", "Asia/Tokyo"},
	{"New York", "America/New_York"},
	{"Los Angeles", "America/Los_Angeles"},
	{"Sydney", "Australia/Sydney"},
	{"Dubai", "Asia/Dubai"},
	{"Singapore", "Asia/Singapore"},
	{"São Paulo", "America/Sao_Paulo"},
	{"Chicago", "America/Chicago"},
	{"Denver", "America/Denver"},
	{"Berlin", "Europe/Berlin"},
	{"Moscow", "Europe/Moscow"},
	{"Shanghai", "Asia/Shanghai"},
	{"Mumbai", "Asia/Kolkata"},
	{"Cairo", "Africa/Cairo"},
	{"Lagos", "Africa/Lagos"},
	{"Toronto", "America/Toronto"},
	{"Vancouver", "America/Vancouver"},
	{"Honolulu", "Pacific/Honolulu"},
	{"Anchorage", "America/Anchorage"},
	{"Auckland", "Pacific/Auckland"},
	{"Bangkok", "Asia/Bangkok"},
	{"Seoul", "Asia/Seoul"},
	{"Jakarta", "Asia/Jakarta"},
	{"Karachi", "Asia/Karachi"},
	{"Dhaka", "Asia/Dhaka"},
	{"Colombo", "Asia/Colombo"},
	{"Nairobi", "Africa/Nairobi"},
	{"Johannesburg", "Africa/Johannesburg"},
	{"Casablanca", "Africa/Casablanca"},
	{"Istanbul", "Europe/Istanbul"},
	{"Riyadh", "Asia/Riyadh"},
	{"Tehran", "Asia/Tehran"},
	{"Kabul", "Asia/Kabul"},
	{"Tashkent", "Asia/Tashkent"},
	{"Kathmandu", "Asia/Kathmandu"},
	{"Yangon", "Asia/Rangoon"},
	{"Phnom Penh", "Asia/Phnom_Penh"},
	{"Ho Chi Minh", "Asia/Ho_Chi_Minh"},
	{"Manila", "Asia/Manila"},
	{"Taipei", "Asia/Taipei"},
	{"Hong Kong", "Asia/Hong_Kong"},
	{"Beijing", "Asia/Shanghai"},
	{"Ulaanbaatar", "Asia/Ulaanbaatar"},
	{"Almaty", "Asia/Almaty"},
	{"Novosibirsk", "Asia/Novosibirsk"},
	{"Vladivostok", "Asia/Vladivostok"},
	{"Helsinki", "Europe/Helsinki"},
	{"Warsaw", "Europe/Warsaw"},
	{"Prague", "Europe/Prague"},
	{"Vienna", "Europe/Vienna"},
	{"Zurich", "Europe/Zurich"},
	{"Amsterdam", "Europe/Amsterdam"},
	{"Brussels", "Europe/Brussels"},
	{"Madrid", "Europe/Madrid"},
	{"Lisbon", "Europe/Lisbon"},
	{"Rome", "Europe/Rome"},
	{"Athens", "Europe/Athens"},
	{"Bucharest", "Europe/Bucharest"},
	{"Kyiv", "Europe/Kyiv"},
	{"Minsk", "Europe/Minsk"},
	{"Stockholm", "Europe/Stockholm"},
	{"Oslo", "Europe/Oslo"},
	{"Copenhagen", "Europe/Copenhagen"},
	{"Reykjavik", "Atlantic/Reykjavik"},
	{"Dublin", "Europe/Dublin"},
	{"Edinburgh", "Europe/London"},
	{"Bogota", "America/Bogota"},
	{"Lima", "America/Lima"},
	{"Santiago", "America/Santiago"},
	{"Buenos Aires", "America/Argentina/Buenos_Aires"},
	{"Caracas", "America/Caracas"},
	{"Mexico City", "America/Mexico_City"},
	{"Guadalajara", "America/Mexico_City"},
	{"Monterrey", "America/Monterrey"},
	{"Havana", "America/Havana"},
	{"Panama", "America/Panama"},
	{"Managua", "America/Managua"},
	{"Guatemala", "America/Guatemala"},
	{"San Jose", "America/Costa_Rica"},
	{"Nassau", "America/Nassau"},
	{"Port of Spain", "America/Port_of_Spain"},
	{"Accra", "Africa/Accra"},
	{"Abidjan", "Africa/Abidjan"},
	{"Dakar", "Africa/Dakar"},
	{"Addis Ababa", "Africa/Addis_Ababa"},
	{"Dar es Salaam", "Africa/Dar_es_Salaam"},
	{"Lusaka", "Africa/Lusaka"},
	{"Harare", "Africa/Harare"},
	{"Tunis", "Africa/Tunis"},
	{"Algiers", "Africa/Algiers"},
	{"Tripoli", "Africa/Tripoli"},
	{"Khartoum", "Africa/Khartoum"},
	{"Kampala", "Africa/Kampala"},
	{"Kinshasa", "Africa/Kinshasa"},
	{"Luanda", "Africa/Luanda"},
	{"Bamako", "Africa/Bamako"},
}

// cityAliases maps common alternate names / countries → IANA timezone IDs.
// Augments the preset list for freeform typing.
var cityAliases = map[string]string{
	// Countries → representative tz
	"france":         "Europe/Paris",
	"germany":        "Europe/Berlin",
	"japan":          "Asia/Tokyo",
	"china":          "Asia/Shanghai",
	"india":          "Asia/Kolkata",
	"australia":      "Australia/Sydney",
	"russia":         "Europe/Moscow",
	"usa":            "America/New_York",
	"us":             "America/New_York",
	"united states":  "America/New_York",
	"uk":             "Europe/London",
	"united kingdom": "Europe/London",
	"england":        "Europe/London",
	"brazil":         "America/Sao_Paulo",
	"canada":         "America/Toronto",
	"mexico":         "America/Mexico_City",
	"spain":          "Europe/Madrid",
	"portugal":       "Europe/Lisbon",
	"italy":          "Europe/Rome",
	"netherlands":    "Europe/Amsterdam",
	"holland":        "Europe/Amsterdam",
	"belgium":        "Europe/Brussels",
	"switzerland":    "Europe/Zurich",
	"austria":        "Europe/Vienna",
	"poland":         "Europe/Warsaw",
	"sweden":         "Europe/Stockholm",
	"norway":         "Europe/Oslo",
	"denmark":        "Europe/Copenhagen",
	"finland":        "Europe/Helsinki",
	"greece":         "Europe/Athens",
	"turkey":         "Europe/Istanbul",
	"egypt":          "Africa/Cairo",
	"nigeria":        "Africa/Lagos",
	"kenya":          "Africa/Nairobi",
	"southafrica":    "Africa/Johannesburg",
	"south africa":   "Africa/Johannesburg",
	"ethiopia":       "Africa/Addis_Ababa",
	"morocco":        "Africa/Casablanca",
	"uae":            "Asia/Dubai",
	"emirates":       "Asia/Dubai",
	"saudi":          "Asia/Riyadh",
	"saudiarabia":    "Asia/Riyadh",
	"saudi arabia":   "Asia/Riyadh",
	"iran":           "Asia/Tehran",
	"pakistan":       "Asia/Karachi",
	"bangladesh":     "Asia/Dhaka",
	"srilanka":       "Asia/Colombo",
	"sri lanka":      "Asia/Colombo",
	"nepal":          "Asia/Kathmandu",
	"myanmar":        "Asia/Rangoon",
	"burma":          "Asia/Rangoon",
	"cambodia":       "Asia/Phnom_Penh",
	"vietnam":        "Asia/Ho_Chi_Minh",
	"philippines":    "Asia/Manila",
	"taiwan":         "Asia/Taipei",
	"hongkong":       "Asia/Hong_Kong",
	"hong kong":      "Asia/Hong_Kong",
	"singapore":      "Asia/Singapore",
	"indonesia":      "Asia/Jakarta",
	"malaysia":       "Asia/Kuala_Lumpur",
	"kuala lumpur":   "Asia/Kuala_Lumpur",
	"thailand":       "Asia/Bangkok",
	"korea":          "Asia/Seoul",
	"southkorea":     "Asia/Seoul",
	"south korea":    "Asia/Seoul",
	"mongolia":       "Asia/Ulaanbaatar",
	"kazakhstan":     "Asia/Almaty",
	"uzbekistan":     "Asia/Tashkent",
	"afghanistan":    "Asia/Kabul",
	"newzealand":     "Pacific/Auckland",
	"new zealand":    "Pacific/Auckland",
	"hawaii":         "Pacific/Honolulu",
	"alaska":         "America/Anchorage",
	"colombia":       "America/Bogota",
	"peru":           "America/Lima",
	"chile":          "America/Santiago",
	"argentina":      "America/Argentina/Buenos_Aires",
	"venezuela":      "America/Caracas",
	"cuba":           "America/Havana",
	"panama":         "America/Panama",
	"costa rica":     "America/Costa_Rica",
	"nicaragua":      "America/Managua",
	"guatemala":      "America/Guatemala",
	"iceland":        "Atlantic/Reykjavik",
	"ireland":        "Europe/Dublin",
	"ukraine":        "Europe/Kyiv",
	"belarus":        "Europe/Minsk",
	"romania":        "Europe/Bucharest",
	"czech":          "Europe/Prague",
	"czechia":        "Europe/Prague",
	"hungary":        "Europe/Budapest",
	"budapest":       "Europe/Budapest",
	"croatia":        "Europe/Zagreb",
	"zagreb":         "Europe/Zagreb",
	"serbia":         "Europe/Belgrade",
	"belgrade":       "Europe/Belgrade",
	"slovakia":       "Europe/Bratislava",
	"bratislava":     "Europe/Bratislava",
	"luxembourg":     "Europe/Luxembourg",
	"latvia":         "Europe/Riga",
	"riga":           "Europe/Riga",
	"estonia":        "Europe/Tallinn",
	"tallinn":        "Europe/Tallinn",
	"lithuania":      "Europe/Vilnius",
	"class":          "Europe/Vilnius",
	"sofia":          "Europe/Sofia",
	"bulgaria":       "Europe/Sofia",
	"new york":       "America/New_York",
	"los angeles":    "America/Los_Angeles",
	"san francisco":  "America/Los_Angeles",
	"seattle":        "America/Los_Angeles",
	"chicago":        "America/Chicago",
	"houston":        "America/Chicago",
	"dallas":         "America/Chicago",
	"denver":         "America/Denver",
	"phoenix":        "America/Phoenix",
	"miami":          "America/New_York",
	"atlanta":        "America/New_York",
	"boston":         "America/New_York",
	"washington":     "America/New_York",
	"dc":             "America/New_York",
	"detroit":        "America/Detroit",
	"minneapolis":    "America/Chicago",
	"st louis":       "America/Chicago",
	"kansas city":    "America/Chicago",
	"nashville":      "America/Chicago",
	"new orleans":    "America/Chicago",
	"memphis":        "America/Chicago",
	"las vegas":      "America/Los_Angeles",
	"portland":       "America/Los_Angeles",
	"sacramento":     "America/Los_Angeles",
	"san diego":      "America/Los_Angeles",
}

// newSmartState creates a SmartState for SmartTypeTime.
func newSmartState() *SmartState {
	labels := make([]string, len(presetLocations))
	locs := make([]string, len(presetLocations))
	for i, p := range presetLocations {
		labels[i] = p.label
		locs[i] = p.iana
	}
	return &SmartState{
		kind:      SmartTypeTime,
		locations: locs,
		locLabels: labels,
		locIdx:    0,
		resolved:  time.Local,
	}
}

func (s *SmartState) loadPreset(idx int) *time.Location {
	if s.locations[idx] == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(s.locations[idx])
	if err != nil {
		return time.Local
	}
	return loc
}

// LocationLabel returns the display label for the current location.
func (s *SmartState) LocationLabel() string {
	if s.query != "" {
		return s.resolved.String()
	}
	return presetLocations[s.locIdx].label
}

// tryResolveQuery attempts to resolve the current query to a *time.Location.
// Priority: alias map → preset label fuzzy → direct IANA load.
func (s *SmartState) tryResolveQuery() {
	q := strings.ToLower(strings.TrimSpace(s.query))
	if q == "" {
		s.resolved = s.loadPreset(s.locIdx)
		return
	}

	// 1. Alias map (exact)
	if iana, ok := cityAliases[q]; ok {
		if loc, err := time.LoadLocation(iana); err == nil {
			s.resolved = loc
			return
		}
	}

	// 2. Prefix match against preset labels
	for i, lbl := range presetLocations {
		if strings.HasPrefix(strings.ToLower(lbl.label), q) {
			s.resolved = s.loadPreset(i)
			return
		}
	}

	// 3. Substring match against alias keys
	for alias, iana := range cityAliases {
		if strings.HasPrefix(alias, q) {
			if loc, err := time.LoadLocation(iana); err == nil {
				s.resolved = loc
				return
			}
		}
	}

	// 4. Attempt direct IANA load (e.g. "America/Toronto")
	if loc, err := time.LoadLocation(s.query); err == nil {
		s.resolved = loc
	}
	// else: keep previous resolved location; don't blank it out mid-type
}
