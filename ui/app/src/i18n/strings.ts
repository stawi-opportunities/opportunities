// UI string catalog. Kept as a flat record per language so missing-key
// fallbacks are a single lookup. English is the canonical source — every
// other language should have the same key set. When adding a new string,
// add the English entry first, then fan it out to the other catalogs.
//
// Translations below are native-speaker approximations intended to ship
// the MVP. Tighten them with a human pass before launch.

export type LangCode =
  | "en"
  | "es"
  | "fr"
  | "de"
  | "pt"
  | "ja"
  | "ar"
  | "zh";

export const SUPPORTED_LANGS: LangCode[] = [
  "en",
  "es",
  "fr",
  "de",
  "pt",
  "ja",
  "ar",
  "zh",
];

// Endonyms — what native speakers call their own language.
export const LANG_LABEL: Record<LangCode, string> = {
  en: "English",
  es: "Español",
  fr: "Français",
  de: "Deutsch",
  pt: "Português",
  ja: "日本語",
  ar: "العربية",
  zh: "中文",
};

// RTL scripts — drives <html dir="rtl"> when the current language is Arabic.
export const RTL_LANGS = new Set<LangCode>(["ar"]);

export interface Strings {
  // nav.jobs is the single top-bar link post-redesign; the others
  // stay typed because other components (old or future) still use
  // them via useI18n.
  "nav.jobs": string;
  "nav.findJobs": string;
  "nav.allJobs": string;
  "nav.search": string;
  "nav.about": string;
  "nav.pricing": string;
  "nav.signIn": string;
  "nav.language": string;
  "nav.categoriesHint": string;

  "cta.applyNow": string;
  "cta.saveJob": string;
  "cta.loadMore": string;
  "cta.subscribe": string;

  "job.postedOn": string;
  "job.remote": string;
  "job.employmentType": string;
  "job.seniority": string;
  "job.salary": string;
  "job.skillsRequired": string;
  "job.skillsNiceToHave": string;
  "job.translatedNotice": string;

  "search.placeholder": string;
  "search.noResults": string;

  "footer.about": string;
  "footer.pricing": string;
  "footer.privacy": string;
  "footer.terms": string;
  "footer.contact": string;
  "footer.rights": string;
}

const en: Strings = {
  "nav.jobs": "Jobs",
  "nav.findJobs": "Find Jobs",
  "nav.allJobs": "All Jobs",
  "nav.search": "Advanced search",
  "nav.about": "About",
  "nav.pricing": "Pricing",
  "nav.signIn": "Sign in",
  "nav.language": "Language",
  "nav.categoriesHint": "Categories load once jobs are indexed.",

  "cta.applyNow": "Apply now",
  "cta.saveJob": "Save job",
  "cta.loadMore": "Load more",
  "cta.subscribe": "Subscribe",

  "job.postedOn": "Posted",
  "job.remote": "Remote",
  "job.employmentType": "Employment type",
  "job.seniority": "Seniority",
  "job.salary": "Salary",
  "job.skillsRequired": "Required skills",
  "job.skillsNiceToHave": "Nice to have",
  "job.translatedNotice": "Automatically translated from the original posting.",

  "search.placeholder": "Search roles, companies, skills…",
  "search.noResults": "No jobs matched your search.",

  "footer.about": "About",
  "footer.pricing": "Pricing",
  "footer.privacy": "Privacy",
  "footer.terms": "Terms",
  "footer.contact": "Contact",
  "footer.rights": "All rights reserved.",
};

const es: Strings = {
  "nav.jobs": "Empleos",
  "nav.findJobs": "Ofertas",
  "nav.allJobs": "Todas las ofertas",
  "nav.search": "Búsqueda avanzada",
  "nav.about": "Nosotros",
  "nav.pricing": "Precios",
  "nav.signIn": "Iniciar sesión",
  "nav.language": "Idioma",
  "nav.categoriesHint": "Las categorías aparecerán cuando haya empleos indexados.",

  "cta.applyNow": "Postular ahora",
  "cta.saveJob": "Guardar empleo",
  "cta.loadMore": "Cargar más",
  "cta.subscribe": "Suscribirse",

  "job.postedOn": "Publicado",
  "job.remote": "Remoto",
  "job.employmentType": "Tipo de empleo",
  "job.seniority": "Nivel",
  "job.salary": "Salario",
  "job.skillsRequired": "Habilidades requeridas",
  "job.skillsNiceToHave": "Deseables",
  "job.translatedNotice": "Traducido automáticamente del anuncio original.",

  "search.placeholder": "Busca roles, empresas, habilidades…",
  "search.noResults": "Ningún empleo coincide con tu búsqueda.",

  "footer.about": "Nosotros",
  "footer.pricing": "Precios",
  "footer.privacy": "Privacidad",
  "footer.terms": "Términos",
  "footer.contact": "Contacto",
  "footer.rights": "Todos los derechos reservados.",
};

const fr: Strings = {
  "nav.jobs": "Emplois",
  "nav.findJobs": "Offres",
  "nav.allJobs": "Toutes les offres",
  "nav.search": "Recherche avancée",
  "nav.about": "À propos",
  "nav.pricing": "Tarifs",
  "nav.signIn": "Se connecter",
  "nav.language": "Langue",
  "nav.categoriesHint": "Les catégories s'afficheront une fois les offres indexées.",

  "cta.applyNow": "Postuler",
  "cta.saveJob": "Enregistrer",
  "cta.loadMore": "Voir plus",
  "cta.subscribe": "S'abonner",

  "job.postedOn": "Publié le",
  "job.remote": "Télétravail",
  "job.employmentType": "Type de contrat",
  "job.seniority": "Niveau",
  "job.salary": "Salaire",
  "job.skillsRequired": "Compétences requises",
  "job.skillsNiceToHave": "Atouts",
  "job.translatedNotice": "Traduit automatiquement de l'offre originale.",

  "search.placeholder": "Recherchez un poste, une entreprise, une compétence…",
  "search.noResults": "Aucune offre ne correspond à votre recherche.",

  "footer.about": "À propos",
  "footer.pricing": "Tarifs",
  "footer.privacy": "Confidentialité",
  "footer.terms": "Conditions",
  "footer.contact": "Contact",
  "footer.rights": "Tous droits réservés.",
};

const de: Strings = {
  "nav.jobs": "Stellen",
  "nav.findJobs": "Stellen",
  "nav.allJobs": "Alle Stellen",
  "nav.search": "Erweiterte Suche",
  "nav.about": "Über uns",
  "nav.pricing": "Preise",
  "nav.signIn": "Anmelden",
  "nav.language": "Sprache",
  "nav.categoriesHint": "Kategorien erscheinen, sobald Stellen indexiert sind.",

  "cta.applyNow": "Jetzt bewerben",
  "cta.saveJob": "Merken",
  "cta.loadMore": "Mehr laden",
  "cta.subscribe": "Abonnieren",

  "job.postedOn": "Veröffentlicht",
  "job.remote": "Remote",
  "job.employmentType": "Beschäftigungsart",
  "job.seniority": "Erfahrungsstufe",
  "job.salary": "Gehalt",
  "job.skillsRequired": "Erforderliche Kenntnisse",
  "job.skillsNiceToHave": "Von Vorteil",
  "job.translatedNotice": "Automatisch aus der Originalanzeige übersetzt.",

  "search.placeholder": "Suche nach Rollen, Unternehmen, Fähigkeiten…",
  "search.noResults": "Keine Stellen entsprechen deiner Suche.",

  "footer.about": "Über uns",
  "footer.pricing": "Preise",
  "footer.privacy": "Datenschutz",
  "footer.terms": "AGB",
  "footer.contact": "Kontakt",
  "footer.rights": "Alle Rechte vorbehalten.",
};

const pt: Strings = {
  "nav.jobs": "Vagas",
  "nav.findJobs": "Vagas",
  "nav.allJobs": "Todas as vagas",
  "nav.search": "Busca avançada",
  "nav.about": "Sobre",
  "nav.pricing": "Planos",
  "nav.signIn": "Entrar",
  "nav.language": "Idioma",
  "nav.categoriesHint": "As categorias aparecem quando houver vagas indexadas.",

  "cta.applyNow": "Candidatar-se",
  "cta.saveJob": "Salvar vaga",
  "cta.loadMore": "Carregar mais",
  "cta.subscribe": "Assinar",

  "job.postedOn": "Publicada em",
  "job.remote": "Remoto",
  "job.employmentType": "Tipo de contrato",
  "job.seniority": "Nível",
  "job.salary": "Salário",
  "job.skillsRequired": "Habilidades exigidas",
  "job.skillsNiceToHave": "Diferenciais",
  "job.translatedNotice": "Traduzido automaticamente do anúncio original.",

  "search.placeholder": "Busque cargos, empresas, habilidades…",
  "search.noResults": "Nenhuma vaga encontrada.",

  "footer.about": "Sobre",
  "footer.pricing": "Planos",
  "footer.privacy": "Privacidade",
  "footer.terms": "Termos",
  "footer.contact": "Contato",
  "footer.rights": "Todos os direitos reservados.",
};

const ja: Strings = {
  "nav.jobs": "求人",
  "nav.findJobs": "求人",
  "nav.allJobs": "すべての求人",
  "nav.search": "詳細検索",
  "nav.about": "会社概要",
  "nav.pricing": "料金",
  "nav.signIn": "ログイン",
  "nav.language": "言語",
  "nav.categoriesHint": "求人が登録されるとカテゴリが表示されます。",

  "cta.applyNow": "応募する",
  "cta.saveJob": "保存",
  "cta.loadMore": "もっと見る",
  "cta.subscribe": "登録する",

  "job.postedOn": "掲載日",
  "job.remote": "リモート",
  "job.employmentType": "雇用形態",
  "job.seniority": "経験レベル",
  "job.salary": "給与",
  "job.skillsRequired": "必須スキル",
  "job.skillsNiceToHave": "歓迎スキル",
  "job.translatedNotice": "元の求人情報から自動翻訳されました。",

  "search.placeholder": "職種・企業・スキルで検索…",
  "search.noResults": "該当する求人が見つかりませんでした。",

  "footer.about": "会社概要",
  "footer.pricing": "料金",
  "footer.privacy": "プライバシー",
  "footer.terms": "利用規約",
  "footer.contact": "お問い合わせ",
  "footer.rights": "All rights reserved.",
};

const ar: Strings = {
  "nav.jobs": "وظائف",
  "nav.findJobs": "الوظائف",
  "nav.allJobs": "كل الوظائف",
  "nav.search": "بحث متقدم",
  "nav.about": "من نحن",
  "nav.pricing": "الأسعار",
  "nav.signIn": "تسجيل الدخول",
  "nav.language": "اللغة",
  "nav.categoriesHint": "ستظهر الفئات بمجرد فهرسة الوظائف.",

  "cta.applyNow": "قدّم الآن",
  "cta.saveJob": "حفظ الوظيفة",
  "cta.loadMore": "عرض المزيد",
  "cta.subscribe": "اشترك",

  "job.postedOn": "نُشرت في",
  "job.remote": "عن بُعد",
  "job.employmentType": "نوع الوظيفة",
  "job.seniority": "المستوى",
  "job.salary": "الراتب",
  "job.skillsRequired": "المهارات المطلوبة",
  "job.skillsNiceToHave": "مهارات إضافية",
  "job.translatedNotice": "تمت الترجمة تلقائيًا من الإعلان الأصلي.",

  "search.placeholder": "ابحث عن وظائف وشركات ومهارات…",
  "search.noResults": "لا توجد وظائف مطابقة.",

  "footer.about": "من نحن",
  "footer.pricing": "الأسعار",
  "footer.privacy": "الخصوصية",
  "footer.terms": "الشروط",
  "footer.contact": "اتصل بنا",
  "footer.rights": "جميع الحقوق محفوظة.",
};

const zh: Strings = {
  "nav.jobs": "职位",
  "nav.findJobs": "招聘",
  "nav.allJobs": "全部职位",
  "nav.search": "高级搜索",
  "nav.about": "关于",
  "nav.pricing": "定价",
  "nav.signIn": "登录",
  "nav.language": "语言",
  "nav.categoriesHint": "职位入库后将显示分类。",

  "cta.applyNow": "立即申请",
  "cta.saveJob": "收藏职位",
  "cta.loadMore": "加载更多",
  "cta.subscribe": "订阅",

  "job.postedOn": "发布时间",
  "job.remote": "远程",
  "job.employmentType": "雇佣类型",
  "job.seniority": "经验要求",
  "job.salary": "薪资",
  "job.skillsRequired": "必备技能",
  "job.skillsNiceToHave": "加分项",
  "job.translatedNotice": "此内容由原始职位自动翻译。",

  "search.placeholder": "搜索职位、公司、技能…",
  "search.noResults": "没有找到匹配的职位。",

  "footer.about": "关于",
  "footer.pricing": "定价",
  "footer.privacy": "隐私",
  "footer.terms": "条款",
  "footer.contact": "联系我们",
  "footer.rights": "保留所有权利。",
};

export const CATALOG: Record<LangCode, Strings> = {
  en,
  es,
  fr,
  de,
  pt,
  ja,
  ar,
  zh,
};

export type StringKey = keyof Strings;
