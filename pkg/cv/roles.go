package cv

import "strings"

// RoleFamily groups target titles into a small set of buckets so
// scoring can use the right keyword list and role-fit reference text.
// Inferring the family from free-text title is intentionally fuzzy —
// missing a category falls back to "general" and we still produce a
// report, just with a looser keyword set.
type RoleFamily string

const (
	FamilyProgramming RoleFamily = "programming"
	FamilyData        RoleFamily = "data"
	FamilyDesign      RoleFamily = "design"
	FamilyMarketing   RoleFamily = "marketing"
	FamilySales       RoleFamily = "sales"
	FamilyDevOps      RoleFamily = "devops"
	FamilyManagement  RoleFamily = "management"
	FamilyGeneral     RoleFamily = "general"
)

// DetectRoleFamily infers the family from a free-text role title
// (e.g. "Full Stack Developer", "Senior Data Engineer", "Product
// Manager"). Tiered keyword match — order matters, first hit wins,
// otherwise the "general" bucket is returned.
func DetectRoleFamily(title string) RoleFamily {
	t := strings.ToLower(title)
	switch {
	case containsAny(t, "devops", "sre", "site reliability", "platform engineer", "infrastructure", "cloud engineer"):
		return FamilyDevOps
	case containsAny(t, "data scientist", "data engineer", "machine learning", "ml engineer", "analyst", "analytics"):
		return FamilyData
	case containsAny(t, "designer", "ux", "ui designer", "product designer", "visual", "graphic"):
		return FamilyDesign
	case containsAny(t, "marketing", "growth", "seo", "content", "social media", "brand"):
		return FamilyMarketing
	case containsAny(t, "sales", "account executive", "business development", "revenue"):
		return FamilySales
	case containsAny(t, "manager", "director", "vp ", "vice president", "head of", "chief ", "lead"):
		return FamilyManagement
	case containsAny(t, "developer", "engineer", "programmer", "software", "backend", "frontend", "full stack", "fullstack", "full-stack"):
		return FamilyProgramming
	default:
		return FamilyGeneral
	}
}

// expectedKeywords is the canonical "ATS expects to see these" list
// per family. Recruiters and parsers reward the exact tokens here;
// the synonyms map below widens matching without diluting the score.
var expectedKeywords = map[RoleFamily][]string{
	FamilyProgramming: {
		"javascript", "typescript", "python", "go", "java", "rust",
		"react", "node.js", "rest", "graphql", "microservices", "api",
		"docker", "kubernetes", "ci/cd", "git", "postgresql", "redis",
		"testing", "aws", "cloud",
	},
	FamilyDevOps: {
		"kubernetes", "docker", "terraform", "ansible", "prometheus",
		"grafana", "ci/cd", "aws", "gcp", "azure", "linux", "bash",
		"observability", "sre", "monitoring", "incident response",
		"infrastructure as code", "automation",
	},
	FamilyData: {
		"python", "sql", "postgresql", "spark", "airflow", "dbt",
		"etl", "machine learning", "tensorflow", "pytorch", "pandas",
		"numpy", "data pipeline", "warehouse", "tableau", "looker",
		"a/b testing", "statistics",
	},
	FamilyDesign: {
		"figma", "sketch", "adobe", "prototyping", "wireframe", "user research",
		"ux", "ui", "design system", "accessibility", "user testing",
		"information architecture", "interaction design",
	},
	FamilyMarketing: {
		"seo", "sem", "content marketing", "google analytics", "ppc",
		"social media", "brand strategy", "copywriting", "email marketing",
		"conversion", "funnel", "hubspot", "campaign",
	},
	FamilySales: {
		"crm", "salesforce", "pipeline", "prospecting", "negotiation",
		"closing", "quota", "account management", "lead generation",
		"saas", "b2b", "outbound", "cold outreach",
	},
	FamilyManagement: {
		"leadership", "strategy", "team management", "budget", "p&l",
		"stakeholder", "okrs", "roadmap", "cross-functional", "hiring",
		"mentoring", "performance reviews",
	},
	FamilyGeneral: {
		"leadership", "communication", "teamwork", "project management",
		"problem solving", "analytical", "collaboration",
	},
}

// keywordSynonyms maps canonical keyword → aliases that should count
// as the same match. Case-insensitive lookup; keep aliases lowercased.
// Additions are strictly additive — a new synonym can't ever raise an
// old CV's score beyond its natural cap because each canonical
// keyword still only contributes once.
var keywordSynonyms = map[string][]string{
	"javascript":   {"js", "ecmascript"},
	"typescript":   {"ts"},
	"go":           {"golang"},
	"node.js":      {"nodejs", "node"},
	"postgresql":   {"postgres", "psql"},
	"kubernetes":   {"k8s"},
	"ci/cd":        {"continuous integration", "continuous delivery", "continuous deployment"},
	"rest":         {"restful", "rest api", "rest apis"},
	"api":          {"apis"},
	"microservices": {"micro-services", "micro services"},
	"machine learning": {"ml", "deep learning"},
	"a/b testing":  {"ab testing", "a-b testing", "split testing"},
	"infrastructure as code": {"iac"},
	"ux":           {"user experience"},
	"ui":           {"user interface"},
	"ppc":          {"pay per click", "paid search"},
	"sem":          {"search engine marketing"},
	"p&l":          {"profit and loss"},
}

// roleReferenceText is the embedding reference for RoleFit. One short
// paragraph per family — cosine-similarity against this is the score.
// Content is deliberately generic so that role fit measures "does
// this CV look like a PROGRAMMING CV" rather than matching a specific
// company's job description.
var roleReferenceText = map[RoleFamily]string{
	FamilyProgramming: "Software engineer building web applications and APIs. Works in TypeScript, Go, or Python, with React on the frontend and services on the backend. Delivers features end-to-end: database schema, REST APIs, frontend integration, tests, CI/CD. Comfortable with Docker, Kubernetes, Postgres, Redis. Reads specs, ships code, iterates on feedback.",
	FamilyDevOps:      "Platform and reliability engineer running production services at scale. Writes Terraform, operates Kubernetes clusters, configures Prometheus dashboards and alerts. On-call rotation, incident response, post-mortems. Designs deployment pipelines, manages secrets and access. Comfortable in AWS or GCP, with bash and Python for automation.",
	FamilyData:        "Data engineer or analyst building pipelines and models. Writes SQL against warehouse tables, Python for transforms, Spark or dbt for scale. Trains machine-learning models in pandas / scikit-learn / PyTorch, runs A/B tests, ships dashboards in Tableau or Looker. Communicates findings to product and business stakeholders.",
	FamilyDesign:      "Product designer shipping user interfaces. Runs user research, drafts wireframes in Figma, prototypes flows, specs components in a design system. Collaborates with engineering on feasibility and accessibility. Makes trade-offs between scope, polish, and deadline.",
	FamilyMarketing:   "Growth marketer running acquisition, conversion, and retention campaigns. Writes copy, runs paid search and social, tracks funnels in Google Analytics or Amplitude. Builds content, plans email sequences, owns A/B tests. Reports on CAC, LTV, and channel ROI.",
	FamilySales:       "Sales rep or account executive managing a pipeline of B2B prospects. Prospects outbound, qualifies leads, runs demos, negotiates contracts, closes deals. Uses Salesforce or HubSpot to track activity. Meets or exceeds quota quarter over quarter.",
	FamilyManagement:  "Engineering, product, or operations manager leading a team. Owns hiring, performance reviews, roadmap, stakeholder communication. Sets OKRs, runs 1:1s, mentors ICs, escalates blockers. Balances execution velocity with team health.",
	FamilyGeneral:     "Professional with demonstrated leadership, communication, and problem-solving skills. Works cross-functionally, manages projects end-to-end, communicates with stakeholders, iterates on feedback.",
}

// containsAny returns true if s contains any of the listed substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
