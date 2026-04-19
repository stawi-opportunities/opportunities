package locale

import (
	"regexp"
	"strings"
)

// InferCountry maps a free-form location string ("San Francisco, CA",
// "Frankfurt am Main", "Remote — UK") onto an ISO-3166 alpha-2 code.
// Empty return means "couldn't tell" — never guess wrong over guess
// nothing, because the tiered feed uses country as a hard filter for
// local/regional sections.
//
// The map is hand-curated from the top source countries + their major
// cities / ISO names / common abbreviations. Add entries here when a
// new market shows up in the crawler output; the canonical pipeline
// calls this whenever the LLM extractor leaves country empty.
func InferCountry(locationText string) string {
	s := strings.ToLower(strings.TrimSpace(locationText))
	if s == "" {
		return ""
	}
	for _, rule := range inferRules {
		if rule.pattern.MatchString(s) {
			return rule.code
		}
	}
	return ""
}

type inferRule struct {
	code    string
	pattern *regexp.Regexp
}

// mustRule compiles a case-insensitive word-boundary regex from a list
// of alternatives. Panics at init on bad patterns — acceptable because
// the list is a constant.
func mustRule(code string, alts ...string) inferRule {
	// Word-boundary on both sides so "india" doesn't match "indiana"
	// and "rabat" doesn't match a street name "rabat road" unless it's
	// its own token. Accented characters are escaped literally —
	// Postgres-style \m / \M don't exist in Go regexp, so we use
	// \b which handles ASCII word boundaries (good enough for
	// city/country names that don't start with accents).
	pat := `\b(?:` + strings.Join(alts, "|") + `)\b`
	return inferRule{code: code, pattern: regexp.MustCompile(`(?i)` + pat)}
}

// inferRules — order matters: more-specific patterns first. Two
// entries referencing the same token (e.g. "dublin" for Ireland vs.
// "dublin, ohio" for US) would need a context-sensitive rule; the
// current set avoids that by using full city names or ISO country
// names only.
var inferRules = []inferRule{
	// North America
	mustRule("US",
		`united states`, `u\.s\.a?\.?`, `usa`,
		`new york`, `nyc`, `san francisco`, `los angeles`, `seattle`, `austin`,
		`boston`, `chicago`, `denver`, `atlanta`, `miami`, `washington`, `dallas`,
		`houston`, `philadelphia`, `san diego`, `portland`, `minneapolis`,
		`nashville`, `raleigh`, `charlotte`, `phoenix`, `detroit`, `columbus`,
		`california`, `texas`, `florida`, `virginia`, `massachusetts`,
		`illinois`, `washington state`, `oregon`, `colorado`, `ohio`, `georgia`,
	),
	mustRule("CA",
		`canada`, `toronto`, `vancouver`, `montreal`, `montréal`,
		`calgary`, `ottawa`, `edmonton`, `quebec`, `québec`, `ontario`,
	),
	mustRule("MX", `mexico`, `méxico`, `mexico city`, `ciudad de méxico`, `guadalajara`, `monterrey`),

	// Europe
	mustRule("GB",
		`united kingdom`, `u\.k\.?`, `england`, `scotland`, `wales`,
		`london`, `manchester`, `edinburgh`, `glasgow`, `bristol`, `leeds`,
		`birmingham`, `cambridge`, `oxford`,
	),
	mustRule("IE", `ireland`, `dublin`, `cork`, `galway`, `limerick`),
	mustRule("DE",
		`germany`, `deutschland`,
		`berlin`, `munich`, `münchen`, `muenchen`, `hamburg`, `frankfurt`,
		`leipzig`, `bocholt`, `cologne`, `köln`, `koeln`, `düsseldorf`, `duesseldorf`,
		`stuttgart`, `bremen`, `dresden`, `nuremberg`, `nürnberg`, `hannover`,
	),
	mustRule("FR", `france`, `paris`, `lyon`, `marseille`, `toulouse`, `bordeaux`, `nantes`, `nice`),
	mustRule("ES", `spain`, `españa`, `espana`, `madrid`, `barcelona`, `valencia`, `sevilla`, `seville`),
	mustRule("IT", `italy`, `italia`, `rome`, `roma`, `milan`, `milano`, `turin`, `torino`, `naples`, `napoli`),
	mustRule("NL", `netherlands`, `nederland`, `holland`, `amsterdam`, `rotterdam`, `utrecht`, `the hague`, `den haag`, `eindhoven`),
	mustRule("BE", `belgium`, `belgique`, `brussels`, `bruxelles`, `antwerp`, `antwerpen`, `ghent`),
	mustRule("PT", `portugal`, `lisbon`, `lisboa`, `porto`),
	mustRule("CH", `switzerland`, `zurich`, `zürich`, `geneva`, `genève`, `basel`, `lausanne`, `bern`),
	mustRule("AT", `austria`, `vienna`, `wien`, `salzburg`, `graz`),
	mustRule("SE", `sweden`, `stockholm`, `gothenburg`, `malmö`, `malmo`),
	mustRule("NO", `norway`, `oslo`, `bergen`, `trondheim`),
	mustRule("DK", `denmark`, `copenhagen`, `københavn`, `kobenhavn`, `aarhus`),
	mustRule("FI", `finland`, `helsinki`, `espoo`, `tampere`),
	mustRule("PL", `poland`, `warsaw`, `warszawa`, `kraków`, `krakow`, `wroclaw`, `wrocław`, `gdansk`, `gdańsk`),
	mustRule("CZ", `czech republic`, `czechia`, `prague`, `praha`, `brno`),
	mustRule("RO", `romania`, `bucharest`, `bucurești`, `cluj`),
	mustRule("GR", `greece`, `athens`, `athína`, `thessaloniki`),

	// Africa
	mustRule("KE", `kenya`, `nairobi`, `mombasa`, `kisumu`, `nakuru`, `eldoret`),
	mustRule("UG", `uganda`, `kampala`, `entebbe`, `jinja`),
	mustRule("TZ", `tanzania`, `dar es salaam`, `dodoma`, `arusha`, `mwanza`),
	mustRule("RW", `rwanda`, `kigali`),
	mustRule("ET", `ethiopia`, `addis ababa`),
	mustRule("NG", `nigeria`, `lagos`, `abuja`, `port harcourt`, `kano`, `ibadan`),
	mustRule("GH", `ghana`, `accra`, `kumasi`, `takoradi`),
	mustRule("ZA", `south africa`, `johannesburg`, `joburg`, `cape town`, `pretoria`, `durban`, `sandton`),
	mustRule("EG", `egypt`, `cairo`, `alexandria`, `giza`),
	mustRule("MA", `morocco`, `casablanca`, `rabat`, `marrakech`, `marrakesh`, `tangier`),
	mustRule("TN", `tunisia`, `tunis`, `sousse`),
	mustRule("DZ", `algeria`, `algiers`),
	mustRule("SN", `senegal`, `dakar`),
	mustRule("CI", `côte d'ivoire`, `cote d'ivoire`, `ivory coast`, `abidjan`),
	mustRule("CM", `cameroon`, `douala`, `yaoundé`, `yaounde`),

	// Middle East
	mustRule("AE", `united arab emirates`, `u\.a\.e\.?`, `uae`, `dubai`, `abu dhabi`, `sharjah`),
	mustRule("SA", `saudi arabia`, `riyadh`, `jeddah`, `dammam`, `mecca`, `makkah`),
	mustRule("QA", `qatar`, `doha`),
	mustRule("IL", `israel`, `tel aviv`, `tel-aviv`, `jerusalem`, `haifa`),
	mustRule("TR", `turkey`, `türkiye`, `istanbul`, `ankara`, `izmir`),

	// Asia
	mustRule("IN",
		`india`,
		`bengaluru`, `bangalore`, `mumbai`, `delhi`, `new delhi`,
		`hyderabad`, `chennai`, `pune`, `kolkata`, `ahmedabad`, `gurugram`, `gurgaon`, `noida`,
	),
	mustRule("PK", `pakistan`, `karachi`, `lahore`, `islamabad`, `rawalpindi`),
	mustRule("BD", `bangladesh`, `dhaka`, `chittagong`),
	mustRule("LK", `sri lanka`, `colombo`),
	mustRule("PH", `philippines`, `manila`, `cebu`, `davao`, `clark`, `pampanga`, `makati`, `taguig`),
	mustRule("ID", `indonesia`, `jakarta`, `surabaya`, `bandung`, `bali`),
	mustRule("SG", `singapore`),
	mustRule("MY", `malaysia`, `kuala lumpur`, `penang`, `johor`),
	mustRule("TH", `thailand`, `bangkok`, `chiang mai`),
	mustRule("VN", `vietnam`, `ho chi minh`, `hanoi`, `da nang`),
	mustRule("JP", `japan`, `tokyo`, `osaka`, `kyoto`, `yokohama`),
	mustRule("KR", `south korea`, `seoul`, `busan`, `incheon`),
	mustRule("CN", `china`, `beijing`, `shanghai`, `shenzhen`, `guangzhou`, `hangzhou`),
	mustRule("HK", `hong kong`),
	mustRule("TW", `taiwan`, `taipei`, `kaohsiung`),

	// Latin America
	mustRule("BR", `brazil`, `brasil`, `são paulo`, `sao paulo`, `rio de janeiro`, `brasília`, `brasilia`, `belo horizonte`),
	mustRule("AR", `argentina`, `buenos aires`, `córdoba`, `cordoba`, `rosario`),
	mustRule("CL", `chile`, `santiago`, `valparaíso`, `valparaiso`),
	mustRule("CO", `colombia`, `bogotá`, `bogota`, `medellín`, `medellin`, `cali`),
	mustRule("PE", `peru`, `lima`, `cusco`, `cuzco`, `arequipa`),
	mustRule("UY", `uruguay`, `montevideo`),

	// Oceania
	mustRule("AU", `australia`, `sydney`, `melbourne`, `brisbane`, `perth`, `adelaide`, `canberra`),
	mustRule("NZ", `new zealand`, `auckland`, `wellington`, `christchurch`),
}
