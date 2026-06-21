// UI string catalog. Kept as a flat record per language so missing-key
// fallbacks are a single lookup. English is the canonical source — every
// other language should have the same key set. When adding a new string,
// add the English entry first, then fan it out to the other catalogs.
//
// Translations below are native-speaker approximations intended to ship
// the MVP. Tighten them with a human pass before launch.

export type LangCode = 'en' | 'es' | 'fr' | 'de' | 'pt' | 'ja' | 'ar' | 'zh';

export const SUPPORTED_LANGS: LangCode[] = ['en', 'es', 'fr', 'de', 'pt', 'ja', 'ar', 'zh'];

// Endonyms — what native speakers call their own language.
export const LANG_LABEL: Record<LangCode, string> = {
  en: 'English',
  es: 'Español',
  fr: 'Français',
  de: 'Deutsch',
  pt: 'Português',
  ja: '日本語',
  ar: 'العربية',
  zh: '中文',
};

// RTL scripts — drives <html dir="rtl"> when the current language is Arabic.
export const RTL_LANGS = new Set<LangCode>(['ar']);

export interface Strings {
  // ---- Navigation ----
  'nav.jobs': string;
  'nav.findJobs': string;
  'nav.allJobs': string;
  'nav.search': string;
  'nav.about': string;
  'nav.faq': string;
  'nav.pricing': string;
  'nav.signIn': string;
  'nav.language': string;
  'nav.categoriesHint': string;

  // ---- Call-to-action buttons ----
  'cta.applyNow': string;
  'cta.saveJob': string;
  'cta.loadMore': string;
  'cta.subscribe': string;
  'cta.redeemNow': string;
  'cta.submitBid': string;
  'cta.share': string;
  'cta.copyLink': string;
  'cta.browseAll': string;
  'cta.tryAgain': string;
  'cta.apply': string;
  'cta.save': string;
  'cta.saved': string;
  'cta.cancel': string;
  'cta.close': string;
  'cta.dismiss': string;
  'cta.retry': string;

  // ---- Job detail ----
  'job.postedOn': string;
  'job.remote': string;
  'job.employmentType': string;
  'job.seniority': string;
  'job.salary': string;
  'job.skillsRequired': string;
  'job.skillsNiceToHave': string;
  'job.translatedNotice': string;

  // ---- Deadline labels ----
  'deadline.closes': string;
  'deadline.expires': string;
  'deadline.applyBy': string;

  // ---- Expired messages ----
  'expired.scholarship': string;
  'expired.tender': string;
  'expired.deal': string;
  'expired.funding': string;
  'expired.job': string;

  // ---- Errors & not-found ----
  'error.notFound': string;
  'error.listingRemoved': string;
  'error.somethingWrong': string;
  'error.couldNotLoad': string;
  'error.categoryLoad': string;
  'error.submitFlag': string;

  // ---- Opportunity kind labels ----
  'kind.job': string;
  'kind.scholarship': string;
  'kind.tender': string;
  'kind.deal': string;
  'kind.funding': string;

  // ---- Common / reusable ----
  'common.home': string;
  'common.categories': string;
  'common.jobs': string;
  'common.loading': string;
  'common.featured': string;

  // ---- Scholarship ----
  'scholarship.minGpa': string;
  'scholarship.eligibleNationalities': string;

  // ---- Tender ----
  'tender.budget': string;
  'tender.bidderEligibility': string;
  'tender.submissionMethod': string;

  // ---- Deal ----
  'deal.percentOff': string;
  'deal.couponCode': string;
  'deal.redeemableIn': string;

  // ---- Funding ----
  'funding.grant': string;
  'funding.orgEligibility': string;
  'funding.targetRegions': string;

  // ---- Search & filters ----
  'search.placeholder': string;
  'search.noResults': string;
  'search.filters': string;
  'search.clearAll': string;
  'search.clear': string;
  'search.showResults': string;
  'search.category': string;
  'search.remote': string;
  'search.hybrid': string;
  'search.onSite': string;
  'search.employmentType': string;
  'search.seniority': string;
  'search.country': string;
  'search.searchPlaceholder': string;
  'search.searchButton': string;
  'search.searchJobs': string;
  'search.sort': string;
  'search.sortRelevance': string;
  'search.sortRecent': string;
  'search.sortQuality': string;
  'search.sortSalary': string;
  'search.uncategorised': string;

  // ---- Flag modal ----
  'flag.trigger': string;
  'flag.title': string;
  'flag.scam': string;
  'flag.expired': string;
  'flag.duplicate': string;
  'flag.spam': string;
  'flag.other': string;
  'flag.thankYou': string;
  'flag.reason': string;
  'flag.details': string;
  'flag.detailsPlaceholder': string;
  'flag.alreadyFlagged': string;
  'flag.signInRequired': string;
  'flag.submitting': string;
  'flag.submitButton': string;

  // ---- Stats ----
  'stats.views': string;
  'stats.applies': string;

  // ---- Locale shard ----
  'locale.showingJobs': string;

  // ---- Category ----
  'category.browseByCategory': string;
  'category.noCategories': string;
  'category.uncategorised': string;
  'category.notFound': string;
  'category.notFoundMessage': string;
  'category.backToAll': string;
  'category.latestRoles': string;
  'category.noRolesOpen': string;
  'category.browseAllJobs': string;

  // ---- Opportunity card / status ----
  'status.applied': string;
  'status.responded': string;
  'status.interview': string;
  'status.offer': string;
  'status.rejected': string;
  'status.hired': string;
  'card.match': string;
  'card.new': string;

  // ---- Cascade / feed ----
  'cascade.yourPreferences': string;
  'cascade.outside': string;
  'cascade.worldwide': string;
  'cascade.loadingMore': string;

  // ---- Feed ----
  'feed.all': string;
  'feed.matches': string;
  'feed.starred': string;
  'feed.applied': string;
  'feed.empty': string;
  'feed.tryAllFilter': string;
  'feed.loadError': string;
  'feed.opportunities': string;

  // ---- Dashboard ----
  'dash.title': string;
  'dash.setupIncomplete': string;
  'dash.finishPayment': string;
  'dash.browseJobs': string;
  'dash.changePlan': string;
  'dash.viewPlans': string;
  'dash.actionNeeded': string;
  'dash.choosePlan': string;
  'dash.editPreferences': string;
  'dash.paymentReceived': string;
  'dash.paymentFailed': string;
  'dash.paymentWaiting': string;
  'dash.matchPreferences': string;
  'dash.matchPreferencesHint': string;
  'dash.saving': string;
  'dash.preferencesSaved': string;
  'dash.preferencesFailed': string;
  'dash.billing': string;
  'dash.active': string;
  'dash.renewsOn': string;
  'dash.changePlanOrCancel': string;
  'dash.signInTitle': string;
  'dash.signInHint': string;
  'dash.yourAgent': string;
  'dash.agentHint': string;
  'dash.emailAgent': string;
  'dash.scheduleCall': string;
  'dash.paymentPastDue': string;
  'dash.updatePayment': string;
  'dash.subCancelled': string;
  'dash.reactivateHint': string;
  'dash.finishSetup': string;
  'dash.matchingHint': string;
  'dash.openingPayment': string;
  'dash.payPerMonth': string;
  'dash.perMonth': string;
  'dash.welcomeTitle': string;
  'dash.welcomeBody': string;
  'dash.welcomeDismiss': string;
  'dash.welcomeTour': string;
  'dash.statusActive': string;
  'dash.statusPastDue': string;
  'dash.statusCancelled': string;
  'dash.emptyFeedTitle': string;
  'dash.emptyFeedHint': string;
  'dash.emptyFeedCompleteProfile': string;
  'dash.emptyFeedSetPreferences': string;
  'dash.emptyFeedBrowseAll': string;
  'dash.celebrationTitle': string;
  'dash.celebrationBody': string;
  'dash.celebrationStep1': string;
  'dash.celebrationStep2': string;
  'dash.celebrationStep3': string;
  'dash.celebrationDismiss': string;
  'onboard.profileSaved': string;

  // ---- Onboarding ----
  'onboard.step': string;
  'onboard.of': string;
  'onboard.aboutYou': string;
  'onboard.yourPreferences': string;
  'onboard.choosePlan': string;
  'onboard.aboutYouHint': string;
  'onboard.preferencesHint': string;
  'onboard.choosePlanHint': string;
  'onboard.targetJobTitle': string;
  'onboard.targetJobTitlePlaceholder': string;
  'onboard.experienceLevel': string;
  'onboard.entry': string;
  'onboard.junior': string;
  'onboard.mid': string;
  'onboard.senior': string;
  'onboard.lead': string;
  'onboard.executive': string;
  'onboard.jobSearchStatus': string;
  'onboard.activelyLooking': string;
  'onboard.openToOffers': string;
  'onboard.casuallyBrowsing': string;
  'onboard.extraInfo': string;
  'onboard.extraInfoPlaceholder': string;
  'onboard.targetSalary': string;
  'onboard.salaryPlaceholder': string;

  'onboard.uploadCV': string;
  'onboard.chooseFile': string;
  'onboard.cvFormats': string;
  'onboard.readyToUpload': string;
  'onboard.remove': string;
  'onboard.cvPrivacy': string;

  'onboard.regions': string;
  'onboard.anywhere': string;
  'onboard.africa': string;
  'onboard.europe': string;
  'onboard.northAmerica': string;
  'onboard.southAmerica': string;
  'onboard.asia': string;
  'onboard.oceania': string;
  'onboard.timezones': string;
  'onboard.languages': string;
  'onboard.jobType': string;
  'onboard.fullTime': string;
  'onboard.partTime': string;
  'onboard.contract': string;
  'onboard.freelance': string;
  'onboard.internship': string;
  'onboard.country': string;
  'onboard.countryPlaceholder': string;
  'onboard.planUpgradeHint': string;
  'onboard.matchesPerWeek': string;
  'onboard.includesAgent': string;
  'onboard.agreeTermsLabel': string;
  'onboard.agreeTermsAnd': string;
  'onboard.paymentRedirectHint': string;
  'onboard.continueToPayment': string;
  'onboard.continue': string;
  'onboard.back': string;
  'onboard.submitting': string;
  'onboard.openingSignIn': string;
  'onboard.draftSaveWarning': string;
  'onboard.validationJobTitle': string;
  'onboard.validationCVOrInfo': string;
  'onboard.validationCV': string;
  'onboard.validationCVSize': string;
  'onboard.validationCVFormat': string;
  'onboard.validationRegion': string;
  'onboard.validationCountry': string;
  'onboard.validationLanguage': string;
  'onboard.validationJobType': string;
  'onboard.validationTerms': string;
  'onboard.cvDropHere': string;
  'onboard.cvDragPrompt': string;
  'onboard.skipPreferences': string;
  'onboard.skipPreferencesHint': string;
  'onboard.features': string;
  'onboard.showLess': string;

  // ---- CTA ----
  'cta.twoMinutes': string;
  'cta.twoMinutesHint': string;
  'cta.getStarted': string;

  // ---- Footer ----
  'footer.jobSeekers': string;
  'footer.browseJobs': string;
  'footer.categories': string;
  'footer.advancedSearch': string;
  'footer.company': string;
  'footer.about': string;
  'footer.faq': string;
  'footer.pricing': string;
  'footer.contact': string;
  'footer.legal': string;
  'footer.termsOfService': string;
  'footer.privacyPolicy': string;
  'footer.privacy': string;
  'footer.terms': string;
  'footer.rights': string;
  'footer.madeBy': string;
  'footer.explore': string;
  'footer.jobs': string;
  'footer.scholarships': string;
  'footer.tenders': string;
  'footer.deals': string;
  'footer.funding': string;
}

// ---------------------------------------------------------------------------
// English (canonical)
// ---------------------------------------------------------------------------
const en: Strings = {
  'nav.jobs': 'Jobs',
  'nav.findJobs': 'Find Jobs',
  'nav.allJobs': 'All Jobs',
  'nav.search': 'Advanced search',
  'nav.about': 'About',
  'nav.faq': 'FAQ',
  'nav.pricing': 'Pricing',
  'nav.signIn': 'Sign in',
  'nav.language': 'Language',
  'nav.categoriesHint': 'Categories load once jobs are indexed.',

  'cta.applyNow': 'Apply now',
  'cta.saveJob': 'Save job',
  'cta.loadMore': 'Load more',
  'cta.subscribe': 'Subscribe',
  'cta.redeemNow': 'Redeem now',
  'cta.submitBid': 'Submit bid',
  'cta.share': 'Share',
  'cta.copyLink': 'Copy link',
  'cta.browseAll': 'Browse all',
  'cta.tryAgain': 'Try again',
  'cta.apply': 'Apply',
  'cta.save': 'Save',
  'cta.saved': 'Saved',
  'cta.cancel': 'Cancel',
  'cta.close': 'Close',
  'cta.dismiss': 'Dismiss',
  'cta.retry': 'Retry',
  'cta.twoMinutes': 'Two minutes to set up.',
  'cta.twoMinutesHint': 'Upload your CV, tell us what you want, we take it from there.',
  'cta.getStarted': 'Get started',

  'job.postedOn': 'Posted',
  'job.remote': 'Remote',
  'job.employmentType': 'Employment type',
  'job.seniority': 'Seniority',
  'job.salary': 'Salary',
  'job.skillsRequired': 'Required skills',
  'job.skillsNiceToHave': 'Nice to have',
  'job.translatedNotice': 'Automatically translated from the original posting.',

  'deadline.closes': 'Closes',
  'deadline.expires': 'Expires',
  'deadline.applyBy': 'Apply by',

  'expired.scholarship': 'This scholarship is no longer accepting applications.',
  'expired.tender': "This tender's submission window has closed.",
  'expired.deal': 'This deal has expired.',
  'expired.funding': 'This funding opportunity is closed.',
  'expired.job': 'This job is no longer accepting applications.',

  'error.notFound': 'not found',
  'error.listingRemoved': 'This listing has been removed or has expired.',
  'error.somethingWrong': 'Something went wrong',
  'error.couldNotLoad': "We couldn't load this listing right now.",
  'error.categoryLoad': "We couldn't load this category.",
  'error.submitFlag': 'Could not submit flag:',

  'kind.job': 'Jobs',
  'kind.scholarship': 'Scholarships',
  'kind.tender': 'Tenders',
  'kind.deal': 'Deals',
  'kind.funding': 'Funding',

  'common.home': 'Home',
  'common.categories': 'Categories',
  'common.jobs': 'jobs',
  'common.loading': 'Loading…',
  'common.featured': 'Featured',

  'scholarship.minGpa': 'Minimum GPA',
  'scholarship.eligibleNationalities': 'Eligible nationalities',

  'tender.budget': 'Budget',
  'tender.bidderEligibility': 'Bidder eligibility',
  'tender.submissionMethod': 'Submission method',

  'deal.percentOff': '% off',
  'deal.couponCode': 'Coupon code',
  'deal.redeemableIn': 'Redeemable in',

  'funding.grant': 'Grant',
  'funding.orgEligibility': 'Organisation eligibility',
  'funding.targetRegions': 'Target regions',

  'search.placeholder': 'Search roles, companies, skills…',
  'search.noResults': 'No jobs matched your search.',
  'search.filters': 'Filters',
  'search.clearAll': 'Clear all',
  'search.clear': 'Clear',
  'search.showResults': 'Show results',
  'search.category': 'Category',
  'search.remote': 'Remote',
  'search.hybrid': 'Hybrid',
  'search.onSite': 'On-site',
  'search.employmentType': 'Employment type',
  'search.seniority': 'Seniority',
  'search.country': 'Country',
  'search.searchPlaceholder': 'Search by title, skill, or company…',
  'search.searchButton': 'Search',
  'search.searchJobs': 'Search jobs',
  'search.sort': 'Sort',
  'search.sortRelevance': 'Relevance',
  'search.sortRecent': 'Most recent',
  'search.sortQuality': 'Highest quality',
  'search.sortSalary': 'Salary: high to low',
  'search.uncategorised': '(uncategorised)',

  'flag.trigger': 'Flag this listing',
  'flag.title': 'Flag this listing',
  'flag.scam': 'Scam or phishing',
  'flag.expired': 'Expired or filled',
  'flag.duplicate': 'Duplicate listing',
  'flag.spam': 'Spam or low-quality',
  'flag.other': 'Other',
  'flag.thankYou': 'Thanks — your report has been recorded and will be reviewed by our moderators.',
  'flag.reason': 'Reason',
  'flag.details': 'Details',
  'flag.detailsPlaceholder': 'What looks wrong about this listing?',
  'flag.alreadyFlagged': "You've already flagged this listing — thanks for keeping the site clean.",
  'flag.signInRequired': 'You need to be signed in to flag a listing.',
  'flag.submitting': 'Submitting…',
  'flag.submitButton': 'Submit flag',

  'stats.views': 'views',
  'stats.applies': 'applies',

  'locale.showingJobs': 'Showing {country} jobs{langs}.',

  'category.browseByCategory': 'Browse by category',
  'category.noCategories': 'No categories yet.',
  'category.uncategorised': 'Uncategorised',
  'category.notFound': 'Category not found',
  'category.notFoundMessage': "That category doesn't exist (yet).",
  'category.backToAll': 'Back to all categories',
  'category.latestRoles':
    'Latest roles in {category} — filtered by category and sorted by recency.',
  'category.noRolesOpen': 'No {category} roles are open right now.',
  'category.browseAllJobs': 'Browse all jobs →',

  'status.applied': 'Applied',
  'status.responded': 'Responded',
  'status.interview': 'Interview scheduled',
  'status.offer': 'Offer received',
  'status.rejected': 'Rejected',
  'status.hired': 'Hired',
  'card.match': '% match',
  'card.new': 'New',

  'cascade.yourPreferences': 'Your preferences',
  'cascade.outside': 'Outside {country}',
  'cascade.worldwide': 'Worldwide',
  'cascade.loadingMore': 'Loading more…',

  'feed.all': 'All',
  'feed.matches': 'Matches',
  'feed.starred': 'Starred',
  'feed.applied': 'Applied',
  'feed.empty': 'Nothing to show here yet.',
  'feed.tryAllFilter': "Try the 'All' filter.",
  'feed.loadError': "Couldn't load your opportunities.",
  'feed.opportunities': 'opportunities',

  'onboard.step': 'Step',
  'onboard.of': 'of',
  'onboard.aboutYou': 'About you',
  'onboard.yourPreferences': 'Your preferences',
  'onboard.choosePlan': 'Choose your plan',
  'onboard.aboutYouHint':
    "Tell us what you're looking for so we can surface the most relevant roles.",
  'onboard.preferencesHint': "We'll filter out roles that don't match your location and timezone.",
  'onboard.choosePlanHint':
    'You can upgrade or cancel any time. All plans include weekly matches to your inbox.',
  'onboard.targetJobTitle': 'Target job title',
  'onboard.targetJobTitlePlaceholder': 'e.g. Senior Software Engineer',
  'onboard.experienceLevel': 'Experience level',
  'onboard.entry': 'Entry (0–2 years)',
  'onboard.junior': 'Junior (2–4 years)',
  'onboard.mid': 'Mid-level (4–6 years)',
  'onboard.senior': 'Senior (6–10 years)',
  'onboard.lead': 'Lead (10+ years)',
  'onboard.executive': 'Executive',
  'onboard.jobSearchStatus': 'Job search status',
  'onboard.activelyLooking': 'Actively looking',
  'onboard.openToOffers': 'Open to offers',
  'onboard.casuallyBrowsing': 'Casually browsing',
  'onboard.extraInfo': 'Additional information',
  'onboard.extraInfoPlaceholder':
    'Anything else you want us to know — certifications, visa status, notice period, etc.',
  'onboard.salaryPlaceholder': 'Annual amount',
  'onboard.targetSalary': 'Target salary',
  'onboard.uploadCV': 'Upload your CV',
  'onboard.chooseFile': 'Choose file',
  'onboard.cvFormats': 'PDF, DOCX, RTF, or TXT · up to 10 MB',
  'onboard.readyToUpload': 'ready to upload',
  'onboard.remove': 'Remove',
  'onboard.cvPrivacy':
    "Your CV is used to match you with relevant jobs. It's never shared with employers without your action.",
  'onboard.regions': "Regions you're able to work in",
  'onboard.anywhere': 'Anywhere',
  'onboard.africa': 'Africa',
  'onboard.europe': 'Europe',
  'onboard.northAmerica': 'North America',
  'onboard.southAmerica': 'South America',
  'onboard.asia': 'Asia',
  'onboard.oceania': 'Oceania',
  'onboard.timezones': 'Preferred time zones',
  'onboard.languages': 'Languages you work in',
  'onboard.jobType': 'Job type',
  'onboard.fullTime': 'Full-time',
  'onboard.partTime': 'Part-time',
  'onboard.contract': 'Contract',
  'onboard.freelance': 'Freelance',
  'onboard.internship': 'Internship',
  'onboard.country': 'Country',
  'onboard.countryPlaceholder': 'e.g. Kenya',
  'onboard.planUpgradeHint':
    'You can upgrade or cancel any time. All plans include weekly matches to your inbox.',
  'onboard.matchesPerWeek': 'Up to {count} matches per week',
  'onboard.includesAgent': 'Includes a dedicated agent',
  'onboard.agreeTermsLabel': 'I agree to the',
  'onboard.agreeTermsAnd': 'and',
  'onboard.paymentRedirectHint':
    "You'll be redirected to our payment partner to complete the subscription. Cancel any time from your dashboard.",
  'onboard.continueToPayment': 'Continue to payment',
  'onboard.continue': 'Continue',
  'onboard.back': 'Back',
  'onboard.submitting': 'Submitting…',
  'onboard.openingSignIn': 'Opening sign-in…',
  'onboard.draftSaveWarning':
    "We couldn't save your progress. Your answers are still here; we'll try again on the next step.",
  'onboard.validationJobTitle': 'Enter a target job title',
  'onboard.validationCVOrInfo': 'Provide a CV or tell us about yourself',
  'onboard.validationCV': 'Upload your CV to continue',
  'onboard.validationCVSize': 'CV must be 10 MB or smaller',
  'onboard.validationCVFormat': 'Upload a PDF, DOCX, RTF, or TXT file',
  'onboard.validationRegion': 'Pick at least one region',
  'onboard.validationCountry': 'Enter your country',
  'onboard.validationLanguage': 'Pick at least one language',
  'onboard.validationJobType': 'Pick at least one job type',
  'onboard.validationTerms': 'Please agree to the Terms before finishing',
  'onboard.cvDropHere': 'Drop your CV here',
  'onboard.cvDragPrompt': 'Drag & drop your CV here, or click to browse',
  'onboard.skipPreferences': "Skip for now — I'll set this up later",
  'onboard.skipPreferencesHint': 'You can always configure these later from your dashboard',
  'onboard.features': 'features',
  'onboard.showLess': 'Show less',

  'dash.title': 'Your dashboard',
  'dash.setupIncomplete': 'Setup incomplete',
  'dash.finishPayment': 'Finish payment to unlock matching.',
  'dash.browseJobs': 'Browse jobs',
  'dash.changePlan': 'Change plan',
  'dash.viewPlans': 'View plans',
  'dash.actionNeeded': 'Action needed',
  'dash.choosePlan': 'Choose a plan',
  'dash.editPreferences': 'Edit preferences',
  'dash.paymentReceived': 'Payment received — your subscription is now active.',
  'dash.paymentFailed': "Payment didn't complete.",
  'dash.paymentWaiting':
    'Waiting for your payment provider to confirm — this usually takes under a minute.',
  'dash.matchPreferences': 'Match preferences',
  'dash.matchPreferencesHint':
    "Opt into the kinds of opportunities you want matched. We'll only run matchers for kinds you've configured.",
  'dash.saving': 'Saving…',
  'dash.preferencesSaved': 'Preferences saved.',
  'dash.preferencesFailed': "Couldn't save preferences.",
  'dash.billing': 'Billing',
  'dash.active': 'Active',
  'dash.renewsOn': 'Renews on',
  'dash.changePlanOrCancel': 'Change plan or cancel →',
  'dash.signInTitle': 'Sign in to continue',
  'dash.signInHint': 'Your dashboard shows your plan, matches, and saved jobs.',
  'dash.yourAgent': 'Your agent',
  'dash.agentHint':
    'Your personal recruiter for the duration of your search. Reach out any time and expect a same-day response.',
  'dash.emailAgent': 'Email',
  'dash.scheduleCall': 'Schedule a 1:1',
  'dash.paymentPastDue': "Your last payment didn't go through",
  'dash.updatePayment': 'Update your payment details to resume matching.',
  'dash.subCancelled': 'Your subscription is cancelled',
  'dash.reactivateHint': 'Re-activate any time to start receiving matches again.',
  'dash.finishSetup': 'Finish setting up your {plan} plan',
  'dash.matchingHint':
    "We'll only run our matching engine on your CV once a plan is active. It takes two minutes.",
  'dash.openingPayment': 'Opening payment…',
  'dash.payPerMonth': 'Pay',
  'dash.perMonth': '/month',
  'dash.welcomeTitle': 'Welcome to Stawi Opportunities!',
  'dash.welcomeBody':
    'We match you with the best opportunities across Africa. Start by completing your profile and setting your preferences.',
  'dash.welcomeDismiss': 'Dismiss',
  'dash.welcomeTour': 'Take a quick tour →',
  'dash.statusActive': 'Active',
  'dash.statusPastDue': 'Past due',
  'dash.statusCancelled': 'Cancelled',
  'dash.emptyFeedTitle': 'No opportunities yet',
  'dash.emptyFeedHint': 'Complete these steps to start receiving matches:',
  'dash.emptyFeedCompleteProfile': 'Complete your profile',
  'dash.emptyFeedSetPreferences': 'Set your preferences',
  'dash.emptyFeedBrowseAll': 'Browse all opportunities',
  'dash.celebrationTitle': "You're all set!",
  'dash.celebrationBody': "Here's what happens next:",
  'dash.celebrationStep1': "We're now matching you with opportunities",
  'dash.celebrationStep2': "You'll receive email notifications for new matches",
  'dash.celebrationStep3': 'Your first matches will appear within 24 hours',
  'dash.celebrationDismiss': 'Got it, show me my dashboard',
  'onboard.profileSaved': 'Profile saved!',

  'footer.jobSeekers': 'Job seekers',
  'footer.browseJobs': 'Browse jobs',
  'footer.categories': 'Categories',
  'footer.advancedSearch': 'Advanced search',
  'footer.company': 'Company',
  'footer.about': 'About',
  'footer.faq': 'FAQ',
  'footer.pricing': 'Pricing',
  'footer.contact': 'Contact',
  'footer.legal': 'Legal',
  'footer.termsOfService': 'Terms of service',
  'footer.privacyPolicy': 'Privacy policy',
  'footer.privacy': 'Privacy',
  'footer.terms': 'Terms',
  'footer.rights': 'All rights reserved.',
  'footer.madeBy': 'A product of',
  'footer.explore': 'Explore',
  'footer.jobs': 'Jobs',
  'footer.scholarships': 'Scholarships',
  'footer.tenders': 'Tenders',
  'footer.deals': 'Deals',
  'footer.funding': 'Funding',
};

// ---------------------------------------------------------------------------
// Spanish
// ---------------------------------------------------------------------------
const es: Strings = {
  'nav.jobs': 'Empleos',
  'nav.findJobs': 'Ofertas',
  'nav.allJobs': 'Todas las ofertas',
  'nav.search': 'Búsqueda avanzada',
  'nav.about': 'Nosotros',
  'nav.faq': 'FAQ',
  'nav.pricing': 'Precios',
  'nav.signIn': 'Iniciar sesión',
  'nav.language': 'Idioma',
  'nav.categoriesHint': 'Las categorías aparecerán cuando haya empleos indexados.',

  'cta.applyNow': 'Postular ahora',
  'cta.saveJob': 'Guardar empleo',
  'cta.loadMore': 'Cargar más',
  'cta.subscribe': 'Suscribirse',
  'cta.redeemNow': 'Canjear ahora',
  'cta.submitBid': 'Enviar oferta',
  'cta.share': 'Compartir',
  'cta.copyLink': 'Copiar enlace',
  'cta.browseAll': 'Ver todo',
  'cta.tryAgain': 'Reintentar',
  'cta.apply': 'Postular',
  'cta.save': 'Guardar',
  'cta.saved': 'Guardado',
  'cta.cancel': 'Cancelar',
  'cta.close': 'Cerrar',
  'cta.dismiss': 'Descartar',
  'cta.retry': 'Reintentar',
  'cta.twoMinutes': 'Listo en dos minutos.',
  'cta.twoMinutesHint': 'Sube tu CV, dinos qué buscas y nosotros nos encargamos del resto.',
  'cta.getStarted': 'Comenzar',

  'job.postedOn': 'Publicado',
  'job.remote': 'Remoto',
  'job.employmentType': 'Tipo de empleo',
  'job.seniority': 'Nivel',
  'job.salary': 'Salario',
  'job.skillsRequired': 'Habilidades requeridas',
  'job.skillsNiceToHave': 'Deseables',
  'job.translatedNotice': 'Traducido automáticamente del anuncio original.',

  'deadline.closes': 'Cierra',
  'deadline.expires': 'Vence',
  'deadline.applyBy': 'Postular antes del',

  'expired.scholarship': 'Esta beca ya no acepta solicitudes.',
  'expired.tender': 'El plazo de esta licitación ha cerrado.',
  'expired.deal': 'Esta oferta ha expirado.',
  'expired.funding': 'Esta oportunidad de financiamiento está cerrada.',
  'expired.job': 'Este empleo ya no acepta solicitudes.',

  'error.notFound': 'no encontrado',
  'error.listingRemoved': 'Este anuncio ha sido eliminado o ha expirado.',
  'error.somethingWrong': 'Algo salió mal',
  'error.couldNotLoad': 'No pudimos cargar este anuncio.',
  'error.categoryLoad': 'No pudimos cargar esta categoría.',
  'error.submitFlag': 'No se pudo enviar el reporte:',

  'kind.job': 'Empleos',
  'kind.scholarship': 'Becas',
  'kind.tender': 'Licitaciones',
  'kind.deal': 'Ofertas',
  'kind.funding': 'Financiamiento',

  'common.home': 'Inicio',
  'common.categories': 'Categorías',
  'common.jobs': 'empleos',
  'common.loading': 'Cargando…',
  'common.featured': 'Destacado',

  'scholarship.minGpa': 'GPA mínimo',
  'scholarship.eligibleNationalities': 'Nacionalidades elegibles',

  'tender.budget': 'Presupuesto',
  'tender.bidderEligibility': 'Elegibilidad del oferente',
  'tender.submissionMethod': 'Método de envío',

  'deal.percentOff': '% de descuento',
  'deal.couponCode': 'Código de cupón',
  'deal.redeemableIn': 'Canjeable en',

  'funding.grant': 'Subvención',
  'funding.orgEligibility': 'Elegibilidad de la organización',
  'funding.targetRegions': 'Regiones objetivo',

  'search.placeholder': 'Busca roles, empresas, habilidades…',
  'search.noResults': 'Ningún empleo coincide con tu búsqueda.',
  'search.filters': 'Filtros',
  'search.clearAll': 'Limpiar todo',
  'search.clear': 'Limpiar',
  'search.showResults': 'Mostrar resultados',
  'search.category': 'Categoría',
  'search.remote': 'Remoto',
  'search.hybrid': 'Híbrido',
  'search.onSite': 'Presencial',
  'search.employmentType': 'Tipo de empleo',
  'search.seniority': 'Nivel',
  'search.country': 'País',
  'search.searchPlaceholder': 'Buscar por título, habilidad o empresa…',
  'search.searchButton': 'Buscar',
  'search.searchJobs': 'Buscar empleos',
  'search.sort': 'Ordenar',
  'search.sortRelevance': 'Relevancia',
  'search.sortRecent': 'Más recientes',
  'search.sortQuality': 'Mayor calidad',
  'search.sortSalary': 'Salario: mayor a menor',
  'search.uncategorised': '(sin categoría)',

  'flag.trigger': 'Reportar este anuncio',
  'flag.title': 'Reportar este anuncio',
  'flag.scam': 'Estafa o phishing',
  'flag.expired': 'Expirado o cubierto',
  'flag.duplicate': 'Anuncio duplicado',
  'flag.spam': 'Spam o baja calidad',
  'flag.other': 'Otro',
  'flag.thankYou':
    'Gracias — tu reporte ha sido registrado y será revisado por nuestros moderadores.',
  'flag.reason': 'Motivo',
  'flag.details': 'Detalles',
  'flag.detailsPlaceholder': '¿Qué tiene de incorrecto este anuncio?',
  'flag.alreadyFlagged': 'Ya reportaste este anuncio — gracias por mantener el sitio limpio.',
  'flag.signInRequired': 'Debes iniciar sesión para reportar un anuncio.',
  'flag.submitting': 'Enviando…',
  'flag.submitButton': 'Enviar reporte',

  'stats.views': 'vistas',
  'stats.applies': 'postulaciones',

  'locale.showingJobs': 'Mostrando empleos en {country}{langs}.',

  'category.browseByCategory': 'Explorar por categoría',
  'category.noCategories': 'Aún no hay categorías.',
  'category.uncategorised': 'Sin categoría',
  'category.notFound': 'Categoría no encontrada',
  'category.notFoundMessage': 'Esa categoría no existe (aún).',
  'category.backToAll': 'Volver a todas las categorías',
  'category.latestRoles':
    'Últimos roles en {category} — filtrados por categoría y ordenados por fecha.',
  'category.noRolesOpen': 'No hay roles de {category} abiertos en este momento.',
  'category.browseAllJobs': 'Ver todos los empleos →',

  'status.applied': 'Postulado',
  'status.responded': 'Respondido',
  'status.interview': 'Entrevista programada',
  'status.offer': 'Oferta recibida',
  'status.rejected': 'Rechazado',
  'status.hired': 'Contratado',
  'card.match': '% coincidencia',
  'card.new': 'Nuevo',

  'cascade.yourPreferences': 'Tus preferencias',
  'cascade.outside': 'Fuera de {country}',
  'cascade.worldwide': 'Mundial',
  'cascade.loadingMore': 'Cargando más…',

  'feed.all': 'Todas',
  'feed.matches': 'Coincidencias',
  'feed.starred': 'Favoritos',
  'feed.applied': 'Postulados',
  'feed.empty': 'Aún no hay nada que mostrar.',
  'feed.tryAllFilter': "Prueba el filtro 'Todas'.",
  'feed.loadError': 'No pudimos cargar tus oportunidades.',
  'feed.opportunities': 'oportunidades',

  'onboard.step': 'Paso',
  'onboard.of': 'de',
  'onboard.aboutYou': 'Sobre ti',
  'onboard.yourPreferences': 'Tus preferencias',
  'onboard.choosePlan': 'Elige tu plan',
  'onboard.aboutYouHint': 'Cuéntanos qué buscas para mostrarte las vacantes más relevantes.',
  'onboard.preferencesHint':
    'Filtraremos las vacantes que no coincidan con tu ubicación y zona horaria.',
  'onboard.choosePlanHint':
    'Puedes cambiar o cancelar en cualquier momento. Todos los planes incluyen coincidencias semanales en tu correo.',
  'onboard.targetJobTitle': 'Puesto objetivo',
  'onboard.targetJobTitlePlaceholder': 'p.ej. Ingeniero de Software Senior',
  'onboard.experienceLevel': 'Nivel de experiencia',
  'onboard.entry': 'Inicial (0–2 años)',
  'onboard.junior': 'Junior (2–4 años)',
  'onboard.mid': 'Intermedio (4–6 años)',
  'onboard.senior': 'Senior (6–10 años)',
  'onboard.lead': 'Líder (10+ años)',
  'onboard.executive': 'Ejecutivo',
  'onboard.jobSearchStatus': 'Estado de búsqueda',
  'onboard.activelyLooking': 'En búsqueda activa',
  'onboard.openToOffers': 'Abierto a ofertas',
  'onboard.casuallyBrowsing': 'Explorando sin prisa',
  'onboard.extraInfo': 'Información adicional',
  'onboard.extraInfoPlaceholder':
    'Cualquier otra cosa que quieras que sepamos: certificaciones, situación de visa, período de preaviso, etc.',
  'onboard.salaryPlaceholder': 'Monto anual',
  'onboard.targetSalary': 'Salario objetivo',
  'onboard.uploadCV': 'Sube tu CV',
  'onboard.chooseFile': 'Elegir archivo',
  'onboard.cvFormats': 'PDF, DOCX, RTF o TXT · hasta 10 MB',
  'onboard.readyToUpload': 'listo para subir',
  'onboard.remove': 'Eliminar',
  'onboard.cvPrivacy':
    'Tu CV se usa para encontrarte vacantes relevantes. Nunca se comparte con empleadores sin tu acción.',
  'onboard.regions': 'Regiones en las que puedes trabajar',
  'onboard.anywhere': 'Cualquier lugar',
  'onboard.africa': 'África',
  'onboard.europe': 'Europa',
  'onboard.northAmerica': 'Norteamérica',
  'onboard.southAmerica': 'Sudamérica',
  'onboard.asia': 'Asia',
  'onboard.oceania': 'Oceanía',
  'onboard.timezones': 'Zonas horarias preferidas',
  'onboard.languages': 'Idiomas en los que trabajas',
  'onboard.jobType': 'Tipo de empleo',
  'onboard.fullTime': 'Tiempo completo',
  'onboard.partTime': 'Medio tiempo',
  'onboard.contract': 'Contrato',
  'onboard.freelance': 'Freelance',
  'onboard.internship': 'Pasantía',
  'onboard.country': 'País',
  'onboard.countryPlaceholder': 'p.ej. México',
  'onboard.planUpgradeHint':
    'Puedes cambiar o cancelar en cualquier momento. Todos los planes incluyen coincidencias semanales en tu correo.',
  'onboard.matchesPerWeek': 'Hasta {count} coincidencias por semana',
  'onboard.includesAgent': 'Incluye un agente dedicado',
  'onboard.agreeTermsLabel': 'Acepto los',
  'onboard.agreeTermsAnd': 'y',
  'onboard.paymentRedirectHint':
    'Serás redirigido a nuestro socio de pagos para completar la suscripción. Cancela en cualquier momento desde tu panel.',
  'onboard.continueToPayment': 'Continuar al pago',
  'onboard.continue': 'Continuar',
  'onboard.back': 'Atrás',
  'onboard.submitting': 'Enviando…',
  'onboard.openingSignIn': 'Abriendo inicio de sesión…',
  'onboard.draftSaveWarning':
    'No pudimos guardar tu progreso. Tus respuestas siguen aquí; lo intentaremos de nuevo en el siguiente paso.',
  'onboard.validationJobTitle': 'Introduce un puesto objetivo',
  'onboard.validationCVOrInfo': 'Proporciona un CV o cuéntanos sobre ti',
  'onboard.validationCV': 'Sube tu CV para continuar',
  'onboard.validationCVSize': 'El CV debe ser de 10 MB o menos',
  'onboard.validationCVFormat': 'Sube un archivo PDF, DOCX, RTF o TXT',
  'onboard.validationRegion': 'Selecciona al menos una región',
  'onboard.validationCountry': 'Introduce tu país',
  'onboard.validationLanguage': 'Selecciona al menos un idioma',
  'onboard.validationJobType': 'Selecciona al menos un tipo de empleo',
  'onboard.validationTerms': 'Acepta los Términos antes de finalizar',
  'onboard.cvDropHere': 'Suelta tu CV aquí',
  'onboard.cvDragPrompt': 'Arrastra y suelta tu CV aquí, o haz clic para explorar',
  'onboard.skipPreferences': 'Saltar por ahora — lo configuraré después',
  'onboard.skipPreferencesHint': 'Siempre puedes configurarlo desde tu panel',
  'onboard.features': 'funciones',
  'onboard.showLess': 'Mostrar menos',

  'dash.title': 'Tu panel',
  'dash.setupIncomplete': 'Configuración incompleta',
  'dash.finishPayment': 'Completa el pago para activar el emparejamiento.',
  'dash.browseJobs': 'Explorar empleos',
  'dash.changePlan': 'Cambiar plan',
  'dash.viewPlans': 'Ver planes',
  'dash.actionNeeded': 'Acción requerida',
  'dash.choosePlan': 'Elige un plan',
  'dash.editPreferences': 'Editar preferencias',
  'dash.paymentReceived': 'Pago recibido — tu suscripción ya está activa.',
  'dash.paymentFailed': 'El pago no se completó.',
  'dash.paymentWaiting':
    'Esperando confirmación de tu proveedor de pago — suele tardar menos de un minuto.',
  'dash.matchPreferences': 'Preferencias de emparejamiento',
  'dash.matchPreferencesHint':
    'Activa los tipos de oportunidades que deseas recibir. Solo ejecutaremos los emparejadores para los tipos que hayas configurado.',
  'dash.saving': 'Guardando…',
  'dash.preferencesSaved': 'Preferencias guardadas.',
  'dash.preferencesFailed': 'No se pudieron guardar las preferencias.',
  'dash.billing': 'Facturación',
  'dash.active': 'Activo',
  'dash.renewsOn': 'Se renueva el',
  'dash.changePlanOrCancel': 'Cambiar plan o cancelar →',
  'dash.signInTitle': 'Inicia sesión para continuar',
  'dash.signInHint': 'Tu panel muestra tu plan, coincidencias y empleos guardados.',
  'dash.yourAgent': 'Tu agente',
  'dash.agentHint':
    'Tu reclutador personal durante toda tu búsqueda. Escríbele cuando quieras y espera una respuesta el mismo día.',
  'dash.emailAgent': 'Correo electrónico',
  'dash.scheduleCall': 'Agendar una llamada 1:1',
  'dash.paymentPastDue': 'Tu último pago no se procesó',
  'dash.updatePayment': 'Actualiza tus datos de pago para reanudar el emparejamiento.',
  'dash.subCancelled': 'Tu suscripción está cancelada',
  'dash.reactivateHint': 'Reactívala en cualquier momento para volver a recibir coincidencias.',
  'dash.finishSetup': 'Termina de configurar tu plan {plan}',
  'dash.matchingHint':
    'Solo ejecutaremos nuestro motor de emparejamiento con tu CV cuando tengas un plan activo. Tarda dos minutos.',
  'dash.openingPayment': 'Abriendo pago…',
  'dash.payPerMonth': 'Pagar',
  'dash.perMonth': '/mes',
  'dash.welcomeTitle': '¡Bienvenido a Stawi Opportunities!',
  'dash.welcomeBody':
    'Te emparejamos con las mejores oportunidades en toda África. Comienza completando tu perfil y configurando tus preferencias.',
  'dash.welcomeDismiss': 'Descartar',
  'dash.welcomeTour': 'Haz un recorrido rápido →',
  'dash.statusActive': 'Activo',
  'dash.statusPastDue': 'Vencido',
  'dash.statusCancelled': 'Cancelado',
  'dash.emptyFeedTitle': 'Aún no hay oportunidades',
  'dash.emptyFeedHint': 'Completa estos pasos para empezar a recibir coincidencias:',
  'dash.emptyFeedCompleteProfile': 'Completa tu perfil',
  'dash.emptyFeedSetPreferences': 'Configura tus preferencias',
  'dash.emptyFeedBrowseAll': 'Explora todas las oportunidades',
  'dash.celebrationTitle': '¡Todo listo!',
  'dash.celebrationBody': 'Esto es lo que sigue:',
  'dash.celebrationStep1': 'Ahora te estamos emparejando con oportunidades',
  'dash.celebrationStep2': 'Recibirás notificaciones por correo de nuevas coincidencias',
  'dash.celebrationStep3': 'Tus primeras coincidencias aparecerán en 24 horas',
  'dash.celebrationDismiss': 'Entendido, muéstrame mi panel',
  'onboard.profileSaved': '¡Perfil guardado!',

  'footer.jobSeekers': 'Candidatos',
  'footer.browseJobs': 'Explorar empleos',
  'footer.categories': 'Categorías',
  'footer.advancedSearch': 'Búsqueda avanzada',
  'footer.company': 'Empresa',
  'footer.about': 'Nosotros',
  'footer.faq': 'FAQ',
  'footer.pricing': 'Precios',
  'footer.contact': 'Contacto',
  'footer.legal': 'Legal',
  'footer.termsOfService': 'Términos de servicio',
  'footer.privacyPolicy': 'Política de privacidad',
  'footer.privacy': 'Privacidad',
  'footer.terms': 'Términos',
  'footer.rights': 'Todos los derechos reservados.',
  'footer.madeBy': 'Un producto de',
  'footer.explore': 'Explorar',
  'footer.jobs': 'Empleos',
  'footer.scholarships': 'Becas',
  'footer.tenders': 'Licitaciones',
  'footer.deals': 'Ofertas',
  'footer.funding': 'Financiamiento',
};

// ---------------------------------------------------------------------------
// French
// ---------------------------------------------------------------------------
const fr: Strings = {
  'nav.jobs': 'Emplois',
  'nav.findJobs': 'Offres',
  'nav.allJobs': 'Toutes les offres',
  'nav.search': 'Recherche avancée',
  'nav.about': 'À propos',
  'nav.faq': 'FAQ',
  'nav.pricing': 'Tarifs',
  'nav.signIn': 'Se connecter',
  'nav.language': 'Langue',
  'nav.categoriesHint': "Les catégories s'afficheront une fois les offres indexées.",

  'cta.applyNow': 'Postuler',
  'cta.saveJob': 'Enregistrer',
  'cta.loadMore': 'Voir plus',
  'cta.subscribe': "S'abonner",
  'cta.redeemNow': 'Utiliser maintenant',
  'cta.submitBid': 'Soumettre une offre',
  'cta.share': 'Partager',
  'cta.copyLink': 'Copier le lien',
  'cta.browseAll': 'Tout parcourir',
  'cta.tryAgain': 'Réessayer',
  'cta.apply': 'Postuler',
  'cta.save': 'Enregistrer',
  'cta.saved': 'Enregistré',
  'cta.cancel': 'Annuler',
  'cta.close': 'Fermer',
  'cta.dismiss': 'Ignorer',
  'cta.retry': 'Réessayer',
  'cta.twoMinutes': 'Prêt en deux minutes.',
  'cta.twoMinutesHint': "Importez votre CV, dites-nous ce que vous cherchez, on s'occupe du reste.",
  'cta.getStarted': 'Commencer',

  'job.postedOn': 'Publié le',
  'job.remote': 'Télétravail',
  'job.employmentType': 'Type de contrat',
  'job.seniority': 'Niveau',
  'job.salary': 'Salaire',
  'job.skillsRequired': 'Compétences requises',
  'job.skillsNiceToHave': 'Atouts',
  'job.translatedNotice': "Traduit automatiquement de l'offre originale.",

  'deadline.closes': 'Clôture le',
  'deadline.expires': 'Expire le',
  'deadline.applyBy': 'Postuler avant le',

  'expired.scholarship': "Cette bourse n'accepte plus de candidatures.",
  'expired.tender': "La période de soumission de cet appel d'offres est terminée.",
  'expired.deal': 'Cette offre a expiré.',
  'expired.funding': 'Cette opportunité de financement est clôturée.',
  'expired.job': "Cette offre d'emploi n'accepte plus de candidatures.",

  'error.notFound': 'introuvable',
  'error.listingRemoved': 'Cette annonce a été supprimée ou a expiré.',
  'error.somethingWrong': 'Une erreur est survenue',
  'error.couldNotLoad': 'Impossible de charger cette annonce.',
  'error.categoryLoad': 'Impossible de charger cette catégorie.',
  'error.submitFlag': "Impossible d'envoyer le signalement :",

  'kind.job': 'Emplois',
  'kind.scholarship': 'Bourses',
  'kind.tender': "Appels d'offres",
  'kind.deal': 'Bons plans',
  'kind.funding': 'Financements',

  'common.home': 'Accueil',
  'common.categories': 'Catégories',
  'common.jobs': 'emplois',
  'common.loading': 'Chargement…',
  'common.featured': 'En vedette',

  'scholarship.minGpa': 'Moyenne minimale',
  'scholarship.eligibleNationalities': 'Nationalités éligibles',

  'tender.budget': 'Budget',
  'tender.bidderEligibility': 'Éligibilité des soumissionnaires',
  'tender.submissionMethod': 'Méthode de soumission',

  'deal.percentOff': '% de réduction',
  'deal.couponCode': 'Code promo',
  'deal.redeemableIn': 'Utilisable en',

  'funding.grant': 'Subvention',
  'funding.orgEligibility': 'Éligibilité des organisations',
  'funding.targetRegions': 'Régions ciblées',

  'search.placeholder': 'Recherchez un poste, une entreprise, une compétence…',
  'search.noResults': 'Aucune offre ne correspond à votre recherche.',
  'search.filters': 'Filtres',
  'search.clearAll': 'Tout effacer',
  'search.clear': 'Effacer',
  'search.showResults': 'Afficher les résultats',
  'search.category': 'Catégorie',
  'search.remote': 'Télétravail',
  'search.hybrid': 'Hybride',
  'search.onSite': 'Sur site',
  'search.employmentType': 'Type de contrat',
  'search.seniority': 'Niveau',
  'search.country': 'Pays',
  'search.searchPlaceholder': 'Rechercher par titre, compétence ou entreprise…',
  'search.searchButton': 'Rechercher',
  'search.searchJobs': 'Rechercher des emplois',
  'search.sort': 'Trier',
  'search.sortRelevance': 'Pertinence',
  'search.sortRecent': 'Plus récentes',
  'search.sortQuality': 'Meilleure qualité',
  'search.sortSalary': 'Salaire : décroissant',
  'search.uncategorised': '(sans catégorie)',

  'flag.trigger': 'Signaler cette annonce',
  'flag.title': 'Signaler cette annonce',
  'flag.scam': 'Arnaque ou hameçonnage',
  'flag.expired': 'Expiré ou pourvu',
  'flag.duplicate': 'Annonce en double',
  'flag.spam': 'Spam ou faible qualité',
  'flag.other': 'Autre',
  'flag.thankYou':
    'Merci — votre signalement a été enregistré et sera examiné par nos modérateurs.',
  'flag.reason': 'Motif',
  'flag.details': 'Détails',
  'flag.detailsPlaceholder': "Qu'est-ce qui semble incorrect dans cette annonce ?",
  'flag.alreadyFlagged':
    'Vous avez déjà signalé cette annonce — merci de contribuer à la qualité du site.',
  'flag.signInRequired': 'Vous devez être connecté pour signaler une annonce.',
  'flag.submitting': 'Envoi…',
  'flag.submitButton': 'Envoyer le signalement',

  'stats.views': 'vues',
  'stats.applies': 'candidatures',

  'locale.showingJobs': 'Affichage des emplois en {country}{langs}.',

  'category.browseByCategory': 'Parcourir par catégorie',
  'category.noCategories': 'Aucune catégorie pour le moment.',
  'category.uncategorised': 'Non catégorisé',
  'category.notFound': 'Catégorie introuvable',
  'category.notFoundMessage': "Cette catégorie n'existe pas (encore).",
  'category.backToAll': 'Retour à toutes les catégories',
  'category.latestRoles':
    'Dernières offres en {category} — filtrées par catégorie et triées par date.',
  'category.noRolesOpen': "Aucun poste de {category} n'est ouvert actuellement.",
  'category.browseAllJobs': 'Voir toutes les offres →',

  'status.applied': 'Postulé',
  'status.responded': 'Répondu',
  'status.interview': 'Entretien programmé',
  'status.offer': 'Offre reçue',
  'status.rejected': 'Refusé',
  'status.hired': 'Embauché',
  'card.match': '% correspondance',
  'card.new': 'Nouveau',

  'cascade.yourPreferences': 'Vos préférences',
  'cascade.outside': 'Hors de {country}',
  'cascade.worldwide': 'Mondial',
  'cascade.loadingMore': 'Chargement…',

  'feed.all': 'Tout',
  'feed.matches': 'Correspondances',
  'feed.starred': 'Favoris',
  'feed.applied': 'Postulé',
  'feed.empty': 'Rien à afficher pour le moment.',
  'feed.tryAllFilter': 'Essayez le filtre « Tout ».',
  'feed.loadError': 'Impossible de charger vos opportunités.',
  'feed.opportunities': 'opportunités',

  'onboard.step': 'Étape',
  'onboard.of': 'sur',
  'onboard.aboutYou': 'À propos de vous',
  'onboard.yourPreferences': 'Vos préférences',
  'onboard.choosePlan': 'Choisissez votre plan',
  'onboard.aboutYouHint':
    'Dites-nous ce que vous recherchez afin que nous puissions afficher les postes les plus pertinents.',
  'onboard.preferencesHint':
    'Nous filtrerons les postes qui ne correspondent pas à votre localisation et fuseau horaire.',
  'onboard.choosePlanHint':
    'Vous pouvez changer ou résilier à tout moment. Tous les plans incluent des correspondances hebdomadaires dans votre boîte mail.',
  'onboard.targetJobTitle': 'Poste recherché',
  'onboard.targetJobTitlePlaceholder': 'ex. Ingénieur logiciel senior',
  'onboard.experienceLevel': "Niveau d'expérience",
  'onboard.entry': 'Débutant (0–2 ans)',
  'onboard.junior': 'Junior (2–4 ans)',
  'onboard.mid': 'Intermédiaire (4–6 ans)',
  'onboard.senior': 'Senior (6–10 ans)',
  'onboard.lead': 'Lead (10+ ans)',
  'onboard.executive': 'Dirigeant',
  'onboard.jobSearchStatus': "Statut de recherche d'emploi",
  'onboard.activelyLooking': 'En recherche active',
  'onboard.openToOffers': 'Ouvert aux opportunités',
  'onboard.casuallyBrowsing': 'En veille',
  'onboard.extraInfo': 'Informations complémentaires',
  'onboard.extraInfoPlaceholder':
    'Tout ce que vous souhaitez nous faire savoir : certifications, statut de visa, préavis, etc.',
  'onboard.salaryPlaceholder': 'Montant annuel',
  'onboard.targetSalary': 'Salaire cible',
  'onboard.uploadCV': 'Importez votre CV',
  'onboard.chooseFile': 'Choisir un fichier',
  'onboard.cvFormats': 'PDF, DOCX, RTF ou TXT · 10 Mo maximum',
  'onboard.readyToUpload': 'prêt à importer',
  'onboard.remove': 'Supprimer',
  'onboard.cvPrivacy':
    "Votre CV est utilisé pour vous proposer des offres pertinentes. Il n'est jamais partagé avec les employeurs sans votre action.",
  'onboard.regions': 'Régions dans lesquelles vous pouvez travailler',
  'onboard.anywhere': 'Partout',
  'onboard.africa': 'Afrique',
  'onboard.europe': 'Europe',
  'onboard.northAmerica': 'Amérique du Nord',
  'onboard.southAmerica': 'Amérique du Sud',
  'onboard.asia': 'Asie',
  'onboard.oceania': 'Océanie',
  'onboard.timezones': 'Fuseaux horaires préférés',
  'onboard.languages': 'Langues dans lesquelles vous travaillez',
  'onboard.jobType': 'Type de poste',
  'onboard.fullTime': 'Temps plein',
  'onboard.partTime': 'Temps partiel',
  'onboard.contract': 'CDD / Contrat',
  'onboard.freelance': 'Freelance',
  'onboard.internship': 'Stage',
  'onboard.country': 'Pays',
  'onboard.countryPlaceholder': 'ex. France',
  'onboard.planUpgradeHint':
    'Vous pouvez changer ou résilier à tout moment. Tous les plans incluent des correspondances hebdomadaires dans votre boîte mail.',
  'onboard.matchesPerWeek': "Jusqu'à {count} correspondances par semaine",
  'onboard.includesAgent': 'Inclut un agent dédié',
  'onboard.agreeTermsLabel': "J'accepte les",
  'onboard.agreeTermsAnd': 'et',
  'onboard.paymentRedirectHint':
    'Vous serez redirigé vers notre partenaire de paiement pour finaliser votre abonnement. Résiliez à tout moment depuis votre tableau de bord.',
  'onboard.continueToPayment': 'Continuer vers le paiement',
  'onboard.continue': 'Continuer',
  'onboard.back': 'Retour',
  'onboard.submitting': 'Envoi en cours…',
  'onboard.openingSignIn': 'Ouverture de la connexion…',
  'onboard.draftSaveWarning':
    "Nous n'avons pas pu sauvegarder votre progression. Vos réponses sont toujours là ; nous réessaierons à l'étape suivante.",
  'onboard.validationJobTitle': 'Entrez un poste recherché',
  'onboard.validationCVOrInfo': 'Fournissez un CV ou parlez-nous de vous',
  'onboard.validationCV': 'Importez votre CV pour continuer',
  'onboard.validationCVSize': 'Le CV doit faire 10 Mo ou moins',
  'onboard.validationCVFormat': 'Importez un fichier PDF, DOCX, RTF ou TXT',
  'onboard.validationRegion': 'Sélectionnez au moins une région',
  'onboard.validationCountry': 'Entrez votre pays',
  'onboard.validationLanguage': 'Sélectionnez au moins une langue',
  'onboard.validationJobType': 'Sélectionnez au moins un type de poste',
  'onboard.validationTerms': 'Veuillez accepter les Conditions avant de terminer',
  'onboard.cvDropHere': 'Déposez votre CV ici',
  'onboard.cvDragPrompt': 'Glissez-déposez votre CV ici, ou cliquez pour parcourir',
  'onboard.skipPreferences': 'Passer pour le moment — je configurerai plus tard',
  'onboard.skipPreferencesHint':
    'Vous pouvez toujours configurer cela depuis votre tableau de bord',
  'onboard.features': 'fonctionnalités',
  'onboard.showLess': 'Afficher moins',

  'dash.title': 'Votre tableau de bord',
  'dash.setupIncomplete': 'Configuration incomplète',
  'dash.finishPayment': 'Finalisez le paiement pour activer le matching.',
  'dash.browseJobs': 'Parcourir les offres',
  'dash.changePlan': 'Changer de plan',
  'dash.viewPlans': 'Voir les plans',
  'dash.actionNeeded': 'Action requise',
  'dash.choosePlan': 'Choisir un plan',
  'dash.editPreferences': 'Modifier les préférences',
  'dash.paymentReceived': 'Paiement reçu — votre abonnement est maintenant actif.',
  'dash.paymentFailed': "Le paiement n'a pas abouti.",
  'dash.paymentWaiting':
    "En attente de confirmation de votre prestataire de paiement — cela prend généralement moins d'une minute.",
  'dash.matchPreferences': 'Préférences de matching',
  'dash.matchPreferencesHint':
    "Activez les types d'opportunités que vous souhaitez recevoir. Nous n'exécuterons les algorithmes que pour les types que vous avez configurés.",
  'dash.saving': 'Enregistrement…',
  'dash.preferencesSaved': 'Préférences enregistrées.',
  'dash.preferencesFailed': "Impossible d'enregistrer les préférences.",
  'dash.billing': 'Facturation',
  'dash.active': 'Actif',
  'dash.renewsOn': 'Renouvellement le',
  'dash.changePlanOrCancel': 'Changer de plan ou résilier →',
  'dash.signInTitle': 'Connectez-vous pour continuer',
  'dash.signInHint':
    'Votre tableau de bord affiche votre plan, vos correspondances et vos offres enregistrées.',
  'dash.yourAgent': 'Votre agent',
  'dash.agentHint':
    'Votre recruteur personnel pendant toute la durée de votre recherche. Contactez-le à tout moment et attendez une réponse dans la journée.',
  'dash.emailAgent': 'E-mail',
  'dash.scheduleCall': 'Planifier un appel 1:1',
  'dash.paymentPastDue': "Votre dernier paiement n'a pas été traité",
  'dash.updatePayment': 'Mettez à jour vos coordonnées de paiement pour reprendre le matching.',
  'dash.subCancelled': 'Votre abonnement est résilié',
  'dash.reactivateHint':
    'Réactivez-le à tout moment pour recommencer à recevoir des correspondances.',
  'dash.finishSetup': 'Terminez la configuration de votre plan {plan}',
  'dash.matchingHint':
    "Nous n'exécuterons notre moteur de matching sur votre CV qu'une fois un plan actif. Cela prend deux minutes.",
  'dash.openingPayment': 'Ouverture du paiement…',
  'dash.payPerMonth': 'Payer',
  'dash.perMonth': '/mois',
  'dash.welcomeTitle': 'Bienvenue sur Stawi Opportunities!',
  'dash.welcomeBody':
    'Nous vous mettons en relation avec les meilleures opportunités en Afrique. Commencez par compléter votre profil et définir vos préférences.',
  'dash.welcomeDismiss': 'Ignorer',
  'dash.welcomeTour': 'Faire un tour rapide →',
  'dash.statusActive': 'Actif',
  'dash.statusPastDue': 'En retard',
  'dash.statusCancelled': 'Annulé',
  'dash.emptyFeedTitle': "Pas encore d'opportunités",
  'dash.emptyFeedHint': 'Suivez ces étapes pour commencer à recevoir des correspondances:',
  'dash.emptyFeedCompleteProfile': 'Complétez votre profil',
  'dash.emptyFeedSetPreferences': 'Définissez vos préférences',
  'dash.emptyFeedBrowseAll': 'Parcourir toutes les opportunités',
  'dash.celebrationTitle': 'Tout est prêt !',
  'dash.celebrationBody': 'Voici la suite :',
  'dash.celebrationStep1': 'Nous vous mettons maintenant en relation avec des opportunités',
  'dash.celebrationStep2':
    'Vous recevrez des notifications par email pour les nouvelles correspondances',
  'dash.celebrationStep3': 'Vos premières correspondances apparaîtront dans les 24 heures',
  'dash.celebrationDismiss': 'Compris, montrez-moi mon tableau de bord',
  'onboard.profileSaved': 'Profil enregistré!',

  'footer.jobSeekers': 'Candidats',
  'footer.browseJobs': 'Parcourir les offres',
  'footer.categories': 'Catégories',
  'footer.advancedSearch': 'Recherche avancée',
  'footer.company': 'Entreprise',
  'footer.about': 'À propos',
  'footer.faq': 'FAQ',
  'footer.pricing': 'Tarifs',
  'footer.contact': 'Contact',
  'footer.legal': 'Mentions légales',
  'footer.termsOfService': "Conditions d'utilisation",
  'footer.privacyPolicy': 'Politique de confidentialité',
  'footer.privacy': 'Confidentialité',
  'footer.terms': 'Conditions',
  'footer.rights': 'Tous droits réservés.',
  'footer.madeBy': 'Un produit de',
  'footer.explore': 'Explorer',
  'footer.jobs': 'Emplois',
  'footer.scholarships': 'Bourses',
  'footer.tenders': "Appels d'offres",
  'footer.deals': 'Bonnes affaires',
  'footer.funding': 'Financement',
};

// ---------------------------------------------------------------------------
// German
// ---------------------------------------------------------------------------
const de: Strings = {
  'nav.jobs': 'Stellen',
  'nav.findJobs': 'Stellen',
  'nav.allJobs': 'Alle Stellen',
  'nav.search': 'Erweiterte Suche',
  'nav.about': 'Über uns',
  'nav.faq': 'FAQ',
  'nav.pricing': 'Preise',
  'nav.signIn': 'Anmelden',
  'nav.language': 'Sprache',
  'nav.categoriesHint': 'Kategorien erscheinen, sobald Stellen indexiert sind.',

  'cta.applyNow': 'Jetzt bewerben',
  'cta.saveJob': 'Merken',
  'cta.loadMore': 'Mehr laden',
  'cta.subscribe': 'Abonnieren',
  'cta.redeemNow': 'Jetzt einlösen',
  'cta.submitBid': 'Angebot abgeben',
  'cta.share': 'Teilen',
  'cta.copyLink': 'Link kopieren',
  'cta.browseAll': 'Alle durchsuchen',
  'cta.tryAgain': 'Erneut versuchen',
  'cta.apply': 'Bewerben',
  'cta.save': 'Speichern',
  'cta.saved': 'Gespeichert',
  'cta.cancel': 'Abbrechen',
  'cta.close': 'Schließen',
  'cta.dismiss': 'Ausblenden',
  'cta.retry': 'Wiederholen',
  'cta.twoMinutes': 'In zwei Minuten startklar.',
  'cta.twoMinutesHint':
    'Laden Sie Ihren Lebenslauf hoch, sagen Sie uns, was Sie suchen, und wir erledigen den Rest.',
  'cta.getStarted': 'Loslegen',

  'job.postedOn': 'Veröffentlicht',
  'job.remote': 'Remote',
  'job.employmentType': 'Beschäftigungsart',
  'job.seniority': 'Erfahrungsstufe',
  'job.salary': 'Gehalt',
  'job.skillsRequired': 'Erforderliche Kenntnisse',
  'job.skillsNiceToHave': 'Von Vorteil',
  'job.translatedNotice': 'Automatisch aus der Originalanzeige übersetzt.',

  'deadline.closes': 'Endet am',
  'deadline.expires': 'Läuft ab am',
  'deadline.applyBy': 'Bewerben bis',

  'expired.scholarship': 'Dieses Stipendium nimmt keine Bewerbungen mehr an.',
  'expired.tender': 'Die Einreichungsfrist dieser Ausschreibung ist abgelaufen.',
  'expired.deal': 'Dieses Angebot ist abgelaufen.',
  'expired.funding': 'Diese Fördermöglichkeit ist geschlossen.',
  'expired.job': 'Diese Stelle nimmt keine Bewerbungen mehr an.',

  'error.notFound': 'nicht gefunden',
  'error.listingRemoved': 'Dieses Inserat wurde entfernt oder ist abgelaufen.',
  'error.somethingWrong': 'Etwas ist schiefgelaufen',
  'error.couldNotLoad': 'Dieses Inserat konnte nicht geladen werden.',
  'error.categoryLoad': 'Diese Kategorie konnte nicht geladen werden.',
  'error.submitFlag': 'Meldung konnte nicht gesendet werden:',

  'kind.job': 'Stellen',
  'kind.scholarship': 'Stipendien',
  'kind.tender': 'Ausschreibungen',
  'kind.deal': 'Angebote',
  'kind.funding': 'Förderungen',

  'common.home': 'Startseite',
  'common.categories': 'Kategorien',
  'common.jobs': 'Stellen',
  'common.loading': 'Laden…',
  'common.featured': 'Empfohlen',

  'scholarship.minGpa': 'Mindest-GPA',
  'scholarship.eligibleNationalities': 'Berechtigte Nationalitäten',

  'tender.budget': 'Budget',
  'tender.bidderEligibility': 'Bieterberechtigung',
  'tender.submissionMethod': 'Einreichungsweg',

  'deal.percentOff': '% Rabatt',
  'deal.couponCode': 'Gutscheincode',
  'deal.redeemableIn': 'Einlösbar in',

  'funding.grant': 'Zuschuss',
  'funding.orgEligibility': 'Organisationsberechtigung',
  'funding.targetRegions': 'Zielregionen',

  'search.placeholder': 'Suche nach Rollen, Unternehmen, Fähigkeiten…',
  'search.noResults': 'Keine Stellen entsprechen deiner Suche.',
  'search.filters': 'Filter',
  'search.clearAll': 'Alles löschen',
  'search.clear': 'Löschen',
  'search.showResults': 'Ergebnisse anzeigen',
  'search.category': 'Kategorie',
  'search.remote': 'Remote',
  'search.hybrid': 'Hybrid',
  'search.onSite': 'Vor Ort',
  'search.employmentType': 'Beschäftigungsart',
  'search.seniority': 'Erfahrungsstufe',
  'search.country': 'Land',
  'search.searchPlaceholder': 'Suche nach Titel, Fähigkeit oder Unternehmen…',
  'search.searchButton': 'Suchen',
  'search.searchJobs': 'Stellen suchen',
  'search.sort': 'Sortieren',
  'search.sortRelevance': 'Relevanz',
  'search.sortRecent': 'Neueste',
  'search.sortQuality': 'Beste Qualität',
  'search.sortSalary': 'Gehalt: absteigend',
  'search.uncategorised': '(ohne Kategorie)',

  'flag.trigger': 'Inserat melden',
  'flag.title': 'Inserat melden',
  'flag.scam': 'Betrug oder Phishing',
  'flag.expired': 'Abgelaufen oder besetzt',
  'flag.duplicate': 'Doppeltes Inserat',
  'flag.spam': 'Spam oder minderwertig',
  'flag.other': 'Sonstiges',
  'flag.thankYou': 'Danke — deine Meldung wurde erfasst und wird von unseren Moderatoren geprüft.',
  'flag.reason': 'Grund',
  'flag.details': 'Details',
  'flag.detailsPlaceholder': 'Was stimmt nicht mit diesem Inserat?',
  'flag.alreadyFlagged': 'Du hast dieses Inserat bereits gemeldet — danke für deinen Beitrag.',
  'flag.signInRequired': 'Du musst angemeldet sein, um ein Inserat zu melden.',
  'flag.submitting': 'Wird gesendet…',
  'flag.submitButton': 'Meldung absenden',

  'stats.views': 'Aufrufe',
  'stats.applies': 'Bewerbungen',

  'locale.showingJobs': 'Stellen in {country}{langs} werden angezeigt.',

  'category.browseByCategory': 'Nach Kategorie durchsuchen',
  'category.noCategories': 'Noch keine Kategorien.',
  'category.uncategorised': 'Ohne Kategorie',
  'category.notFound': 'Kategorie nicht gefunden',
  'category.notFoundMessage': 'Diese Kategorie existiert (noch) nicht.',
  'category.backToAll': 'Zurück zu allen Kategorien',
  'category.latestRoles':
    'Neueste Stellen in {category} — nach Kategorie gefiltert und nach Datum sortiert.',
  'category.noRolesOpen': 'Derzeit sind keine {category}-Stellen offen.',
  'category.browseAllJobs': 'Alle Stellen durchsuchen →',

  'status.applied': 'Beworben',
  'status.responded': 'Geantwortet',
  'status.interview': 'Vorstellungsgespräch geplant',
  'status.offer': 'Angebot erhalten',
  'status.rejected': 'Abgelehnt',
  'status.hired': 'Eingestellt',
  'card.match': '% Übereinstimmung',
  'card.new': 'Neu',

  'cascade.yourPreferences': 'Deine Präferenzen',
  'cascade.outside': 'Außerhalb von {country}',
  'cascade.worldwide': 'Weltweit',
  'cascade.loadingMore': 'Mehr laden…',

  'feed.all': 'Alle',
  'feed.matches': 'Treffer',
  'feed.starred': 'Gemerkt',
  'feed.applied': 'Beworben',
  'feed.empty': 'Hier gibt es noch nichts zu sehen.',
  'feed.tryAllFilter': 'Versuche den Filter „Alle“.',
  'feed.loadError': 'Deine Chancen konnten nicht geladen werden.',
  'feed.opportunities': 'M\u00f6glichkeiten',

  'onboard.step': 'Schritt',
  'onboard.of': 'von',
  'onboard.aboutYou': 'Über dich',
  'onboard.yourPreferences': 'Deine Präferenzen',
  'onboard.choosePlan': 'Wähle deinen Plan',
  'onboard.aboutYouHint':
    'Erzähle uns, was du suchst, damit wir dir die passendsten Stellen zeigen können.',
  'onboard.preferencesHint':
    'Wir filtern Stellen heraus, die nicht zu deinem Standort und deiner Zeitzone passen.',
  'onboard.choosePlanHint':
    'Du kannst jederzeit upgraden oder kündigen. Alle Pläne beinhalten wöchentliche Treffer in deinem Posteingang.',
  'onboard.targetJobTitle': 'Gesuchte Berufsbezeichnung',
  'onboard.targetJobTitlePlaceholder': 'z.B. Senior Software Engineer',
  'onboard.experienceLevel': 'Erfahrungsstufe',
  'onboard.entry': 'Einstieg (0–2 Jahre)',
  'onboard.junior': 'Junior (2–4 Jahre)',
  'onboard.mid': 'Mittelstufe (4–6 Jahre)',
  'onboard.senior': 'Senior (6–10 Jahre)',
  'onboard.lead': 'Lead (10+ Jahre)',
  'onboard.executive': 'Führungskraft',
  'onboard.jobSearchStatus': 'Status der Jobsuche',
  'onboard.activelyLooking': 'Aktiv auf der Suche',
  'onboard.openToOffers': 'Offen für Angebote',
  'onboard.casuallyBrowsing': 'Gelegentlich stöbernd',
  'onboard.extraInfo': 'Zusätzliche Informationen',
  'onboard.extraInfoPlaceholder':
    'Alles Weitere, das wir wissen sollten — Zertifizierungen, Visastatus, Kündigungsfrist usw.',
  'onboard.salaryPlaceholder': 'Jahresbetrag',
  'onboard.targetSalary': 'Zielgehalt',
  'onboard.uploadCV': 'Lade deinen Lebenslauf hoch',
  'onboard.chooseFile': 'Datei auswählen',
  'onboard.cvFormats': 'PDF, DOCX, RTF oder TXT · bis zu 10 MB',
  'onboard.readyToUpload': 'bereit zum Hochladen',
  'onboard.remove': 'Entfernen',
  'onboard.cvPrivacy':
    'Dein Lebenslauf wird verwendet, um passende Stellen zu finden. Er wird nie ohne dein Zutun an Arbeitgeber weitergegeben.',
  'onboard.regions': 'Regionen, in denen du arbeiten kannst',
  'onboard.anywhere': 'Überall',
  'onboard.africa': 'Afrika',
  'onboard.europe': 'Europa',
  'onboard.northAmerica': 'Nordamerika',
  'onboard.southAmerica': 'Südamerika',
  'onboard.asia': 'Asien',
  'onboard.oceania': 'Ozeanien',
  'onboard.timezones': 'Bevorzugte Zeitzonen',
  'onboard.languages': 'Sprachen, in denen du arbeitest',
  'onboard.jobType': 'Beschäftigungsart',
  'onboard.fullTime': 'Vollzeit',
  'onboard.partTime': 'Teilzeit',
  'onboard.contract': 'Befristet',
  'onboard.freelance': 'Freiberuflich',
  'onboard.internship': 'Praktikum',
  'onboard.country': 'Land',
  'onboard.countryPlaceholder': 'z.B. Deutschland',
  'onboard.planUpgradeHint':
    'Du kannst jederzeit upgraden oder kündigen. Alle Pläne beinhalten wöchentliche Treffer in deinem Posteingang.',
  'onboard.matchesPerWeek': 'Bis zu {count} Treffer pro Woche',
  'onboard.includesAgent': 'Inklusive persönlichem Agenten',
  'onboard.agreeTermsLabel': 'Ich stimme den',
  'onboard.agreeTermsAnd': 'und',
  'onboard.paymentRedirectHint':
    'Du wirst zu unserem Zahlungspartner weitergeleitet, um das Abonnement abzuschließen. Kündige jederzeit über dein Dashboard.',
  'onboard.continueToPayment': 'Weiter zur Zahlung',
  'onboard.continue': 'Weiter',
  'onboard.back': 'Zurück',
  'onboard.submitting': 'Wird gesendet…',
  'onboard.openingSignIn': 'Anmeldung wird geöffnet…',
  'onboard.draftSaveWarning':
    'Dein Fortschritt konnte nicht gespeichert werden. Deine Antworten sind noch da; wir versuchen es beim nächsten Schritt erneut.',
  'onboard.validationJobTitle': 'Gib eine gesuchte Berufsbezeichnung ein',
  'onboard.validationCVOrInfo': 'Lade einen Lebenslauf hoch oder erzähle uns etwas über dich',
  'onboard.validationCV': 'Lade deinen Lebenslauf hoch, um fortzufahren',
  'onboard.validationCVSize': 'Der Lebenslauf darf maximal 10 MB groß sein',
  'onboard.validationCVFormat': 'Lade eine PDF-, DOCX-, RTF- oder TXT-Datei hoch',
  'onboard.validationRegion': 'Wähle mindestens eine Region',
  'onboard.validationCountry': 'Gib dein Land ein',
  'onboard.validationLanguage': 'Wähle mindestens eine Sprache',
  'onboard.validationJobType': 'Wähle mindestens eine Beschäftigungsart',
  'onboard.validationTerms': 'Bitte stimme den Nutzungsbedingungen zu, bevor du fortfährst',
  'onboard.cvDropHere': 'Lebenslauf hier ablegen',
  'onboard.cvDragPrompt': 'Ziehe deinen Lebenslauf hierher oder klicke zum Durchsuchen',
  'onboard.skipPreferences': 'Überspringen — ich richte das später ein',
  'onboard.skipPreferencesHint': 'Du kannst dies jederzeit in deinem Dashboard konfigurieren',
  'onboard.features': 'Funktionen',
  'onboard.showLess': 'Weniger anzeigen',

  'dash.title': 'Dein Dashboard',
  'dash.setupIncomplete': 'Einrichtung unvollständig',
  'dash.finishPayment': 'Schließe die Zahlung ab, um das Matching zu aktivieren.',
  'dash.browseJobs': 'Stellen durchsuchen',
  'dash.changePlan': 'Plan ändern',
  'dash.viewPlans': 'Pläne ansehen',
  'dash.actionNeeded': 'Handlung erforderlich',
  'dash.choosePlan': 'Wähle einen Plan',
  'dash.editPreferences': 'Präferenzen bearbeiten',
  'dash.paymentReceived': 'Zahlung erhalten — dein Abonnement ist jetzt aktiv.',
  'dash.paymentFailed': 'Die Zahlung wurde nicht abgeschlossen.',
  'dash.paymentWaiting':
    'Wir warten auf die Bestätigung deines Zahlungsanbieters — das dauert in der Regel weniger als eine Minute.',
  'dash.matchPreferences': 'Matching-Präferenzen',
  'dash.matchPreferencesHint':
    'Aktiviere die Arten von Chancen, die du empfangen möchtest. Wir führen Matcher nur für die von dir konfigurierten Arten aus.',
  'dash.saving': 'Wird gespeichert…',
  'dash.preferencesSaved': 'Präferenzen gespeichert.',
  'dash.preferencesFailed': 'Präferenzen konnten nicht gespeichert werden.',
  'dash.billing': 'Abrechnung',
  'dash.active': 'Aktiv',
  'dash.renewsOn': 'Verlängert sich am',
  'dash.changePlanOrCancel': 'Plan ändern oder kündigen →',
  'dash.signInTitle': 'Melde dich an, um fortzufahren',
  'dash.signInHint':
    'Dein Dashboard zeigt deinen Plan, Übereinstimmungen und gespeicherte Stellen.',
  'dash.yourAgent': 'Dein Agent',
  'dash.agentHint':
    'Dein persönlicher Recruiter für die Dauer deiner Suche. Melde dich jederzeit und erwarte eine Antwort am selben Tag.',
  'dash.emailAgent': 'E-Mail',
  'dash.scheduleCall': '1:1-Gespräch planen',
  'dash.paymentPastDue': 'Deine letzte Zahlung wurde nicht verarbeitet',
  'dash.updatePayment': 'Aktualisiere deine Zahlungsdaten, um das Matching fortzusetzen.',
  'dash.subCancelled': 'Dein Abonnement ist gekündigt',
  'dash.reactivateHint': 'Reaktiviere es jederzeit, um wieder Übereinstimmungen zu erhalten.',
  'dash.finishSetup': 'Schließe die Einrichtung deines {plan}-Plans ab',
  'dash.matchingHint':
    'Wir führen unser Matching erst aus, wenn ein Plan aktiv ist. Es dauert zwei Minuten.',
  'dash.openingPayment': 'Zahlung wird geöffnet…',
  'dash.payPerMonth': 'Zahlen',
  'dash.perMonth': '/Monat',
  'dash.welcomeTitle': 'Willkommen bei Stawi Opportunities!',
  'dash.welcomeBody':
    'Wir verbinden Sie mit den besten Möglichkeiten in ganz Afrika. Vervollständigen Sie Ihr Profil und legen Sie Ihre Präferenzen fest.',
  'dash.welcomeDismiss': 'Schließen',
  'dash.welcomeTour': 'Kurze Tour starten →',
  'dash.statusActive': 'Aktiv',
  'dash.statusPastDue': 'Überfällig',
  'dash.statusCancelled': 'Gekündigt',
  'dash.emptyFeedTitle': 'Noch keine Möglichkeiten',
  'dash.emptyFeedHint': 'Führen Sie diese Schritte aus, um Übereinstimmungen zu erhalten:',
  'dash.emptyFeedCompleteProfile': 'Profil vervollständigen',
  'dash.emptyFeedSetPreferences': 'Präferenzen festlegen',
  'dash.emptyFeedBrowseAll': 'Alle Möglichkeiten durchsuchen',
  'dash.celebrationTitle': 'Alles bereit!',
  'dash.celebrationBody': 'So geht es weiter:',
  'dash.celebrationStep1': 'Wir gleichen Sie jetzt mit Möglichkeiten ab',
  'dash.celebrationStep2': 'Sie erhalten E-Mail-Benachrichtigungen über neue Übereinstimmungen',
  'dash.celebrationStep3': 'Ihre ersten Übereinstimmungen erscheinen innerhalb von 24 Stunden',
  'dash.celebrationDismiss': 'Verstanden, zeigen Sie mir mein Dashboard',
  'onboard.profileSaved': 'Profil gespeichert!',

  'footer.jobSeekers': 'Jobsuchende',
  'footer.browseJobs': 'Stellen durchsuchen',
  'footer.categories': 'Kategorien',
  'footer.advancedSearch': 'Erweiterte Suche',
  'footer.company': 'Unternehmen',
  'footer.about': 'Über uns',
  'footer.faq': 'FAQ',
  'footer.pricing': 'Preise',
  'footer.contact': 'Kontakt',
  'footer.legal': 'Rechtliches',
  'footer.termsOfService': 'Nutzungsbedingungen',
  'footer.privacyPolicy': 'Datenschutzerklärung',
  'footer.privacy': 'Datenschutz',
  'footer.terms': 'AGB',
  'footer.rights': 'Alle Rechte vorbehalten.',
  'footer.madeBy': 'Ein Produkt von',
  'footer.explore': 'Entdecken',
  'footer.jobs': 'Stellen',
  'footer.scholarships': 'Stipendien',
  'footer.tenders': 'Ausschreibungen',
  'footer.deals': 'Angebote',
  'footer.funding': 'Förderung',
};

// ---------------------------------------------------------------------------
// Portuguese
// ---------------------------------------------------------------------------
const pt: Strings = {
  'nav.jobs': 'Vagas',
  'nav.findJobs': 'Vagas',
  'nav.allJobs': 'Todas as vagas',
  'nav.search': 'Busca avançada',
  'nav.about': 'Sobre',
  'nav.faq': 'FAQ',
  'nav.pricing': 'Planos',
  'nav.signIn': 'Entrar',
  'nav.language': 'Idioma',
  'nav.categoriesHint': 'As categorias aparecem quando houver vagas indexadas.',

  'cta.applyNow': 'Candidatar-se',
  'cta.saveJob': 'Salvar vaga',
  'cta.loadMore': 'Carregar mais',
  'cta.subscribe': 'Assinar',
  'cta.redeemNow': 'Resgatar agora',
  'cta.submitBid': 'Enviar proposta',
  'cta.share': 'Compartilhar',
  'cta.copyLink': 'Copiar link',
  'cta.browseAll': 'Ver tudo',
  'cta.tryAgain': 'Tentar novamente',
  'cta.apply': 'Candidatar-se',
  'cta.save': 'Salvar',
  'cta.saved': 'Salvo',
  'cta.cancel': 'Cancelar',
  'cta.close': 'Fechar',
  'cta.dismiss': 'Dispensar',
  'cta.retry': 'Tentar novamente',
  'cta.twoMinutes': 'Pronto em dois minutos.',
  'cta.twoMinutesHint': 'Envie seu currículo, diga-nos o que procura e nós cuidamos do resto.',
  'cta.getStarted': 'Começar',

  'job.postedOn': 'Publicada em',
  'job.remote': 'Remoto',
  'job.employmentType': 'Tipo de contrato',
  'job.seniority': 'Nível',
  'job.salary': 'Salário',
  'job.skillsRequired': 'Habilidades exigidas',
  'job.skillsNiceToHave': 'Diferenciais',
  'job.translatedNotice': 'Traduzido automaticamente do anúncio original.',

  'deadline.closes': 'Encerra em',
  'deadline.expires': 'Expira em',
  'deadline.applyBy': 'Candidatar até',

  'expired.scholarship': 'Esta bolsa não aceita mais candidaturas.',
  'expired.tender': 'O prazo de submissão desta licitação foi encerrado.',
  'expired.deal': 'Esta oferta expirou.',
  'expired.funding': 'Esta oportunidade de financiamento está encerrada.',
  'expired.job': 'Esta vaga não aceita mais candidaturas.',

  'error.notFound': 'não encontrado',
  'error.listingRemoved': 'Este anúncio foi removido ou expirou.',
  'error.somethingWrong': 'Algo deu errado',
  'error.couldNotLoad': 'Não foi possível carregar este anúncio.',
  'error.categoryLoad': 'Não foi possível carregar esta categoria.',
  'error.submitFlag': 'Não foi possível enviar o reporte:',

  'kind.job': 'Vagas',
  'kind.scholarship': 'Bolsas',
  'kind.tender': 'Licitações',
  'kind.deal': 'Ofertas',
  'kind.funding': 'Financiamentos',

  'common.home': 'Início',
  'common.categories': 'Categorias',
  'common.jobs': 'vagas',
  'common.loading': 'Carregando…',
  'common.featured': 'Destaque',

  'scholarship.minGpa': 'GPA mínimo',
  'scholarship.eligibleNationalities': 'Nacionalidades elegíveis',

  'tender.budget': 'Orçamento',
  'tender.bidderEligibility': 'Elegibilidade do licitante',
  'tender.submissionMethod': 'Método de submissão',

  'deal.percentOff': '% de desconto',
  'deal.couponCode': 'Código do cupom',
  'deal.redeemableIn': 'Resgatável em',

  'funding.grant': 'Subvenção',
  'funding.orgEligibility': 'Elegibilidade da organização',
  'funding.targetRegions': 'Regiões-alvo',

  'search.placeholder': 'Busque cargos, empresas, habilidades…',
  'search.noResults': 'Nenhuma vaga encontrada.',
  'search.filters': 'Filtros',
  'search.clearAll': 'Limpar tudo',
  'search.clear': 'Limpar',
  'search.showResults': 'Mostrar resultados',
  'search.category': 'Categoria',
  'search.remote': 'Remoto',
  'search.hybrid': 'Híbrido',
  'search.onSite': 'Presencial',
  'search.employmentType': 'Tipo de contrato',
  'search.seniority': 'Nível',
  'search.country': 'País',
  'search.searchPlaceholder': 'Buscar por título, habilidade ou empresa…',
  'search.searchButton': 'Buscar',
  'search.searchJobs': 'Buscar vagas',
  'search.sort': 'Ordenar',
  'search.sortRelevance': 'Relevância',
  'search.sortRecent': 'Mais recentes',
  'search.sortQuality': 'Melhor qualidade',
  'search.sortSalary': 'Salário: maior para menor',
  'search.uncategorised': '(sem categoria)',

  'flag.trigger': 'Denunciar este anúncio',
  'flag.title': 'Denunciar este anúncio',
  'flag.scam': 'Golpe ou phishing',
  'flag.expired': 'Expirado ou preenchido',
  'flag.duplicate': 'Anúncio duplicado',
  'flag.spam': 'Spam ou baixa qualidade',
  'flag.other': 'Outro',
  'flag.thankYou':
    'Obrigado — sua denúncia foi registrada e será analisada por nossos moderadores.',
  'flag.reason': 'Motivo',
  'flag.details': 'Detalhes',
  'flag.detailsPlaceholder': 'O que parece errado neste anúncio?',
  'flag.alreadyFlagged': 'Você já denunciou este anúncio — obrigado por manter o site limpo.',
  'flag.signInRequired': 'Você precisa estar logado para denunciar um anúncio.',
  'flag.submitting': 'Enviando…',
  'flag.submitButton': 'Enviar denúncia',

  'stats.views': 'visualizações',
  'stats.applies': 'candidaturas',

  'locale.showingJobs': 'Exibindo vagas em {country}{langs}.',

  'category.browseByCategory': 'Explorar por categoria',
  'category.noCategories': 'Nenhuma categoria ainda.',
  'category.uncategorised': 'Sem categoria',
  'category.notFound': 'Categoria não encontrada',
  'category.notFoundMessage': 'Essa categoria não existe (ainda).',
  'category.backToAll': 'Voltar para todas as categorias',
  'category.latestRoles':
    'Últimas vagas em {category} — filtradas por categoria e ordenadas por data.',
  'category.noRolesOpen': 'Não há vagas de {category} abertas no momento.',
  'category.browseAllJobs': 'Ver todas as vagas →',

  'status.applied': 'Candidatado',
  'status.responded': 'Respondido',
  'status.interview': 'Entrevista agendada',
  'status.offer': 'Oferta recebida',
  'status.rejected': 'Rejeitado',
  'status.hired': 'Contratado',
  'card.match': '% compatível',
  'card.new': 'Novo',

  'cascade.yourPreferences': 'Suas preferências',
  'cascade.outside': 'Fora de {country}',
  'cascade.worldwide': 'Mundial',
  'cascade.loadingMore': 'Carregando mais…',

  'feed.all': 'Todas',
  'feed.matches': 'Compatíveis',
  'feed.starred': 'Favoritas',
  'feed.applied': 'Candidatadas',
  'feed.empty': 'Nada para mostrar por enquanto.',
  'feed.tryAllFilter': "Experimente o filtro 'Todas'.",
  'feed.loadError': 'Não foi possível carregar suas oportunidades.',
  'feed.opportunities': 'oportunidades',

  'onboard.step': 'Passo',
  'onboard.of': 'de',
  'onboard.aboutYou': 'Sobre você',
  'onboard.yourPreferences': 'Suas preferências',
  'onboard.choosePlan': 'Escolha seu plano',
  'onboard.aboutYouHint': 'Conte-nos o que você procura para exibirmos as vagas mais relevantes.',
  'onboard.preferencesHint':
    'Filtraremos vagas que não correspondam à sua localização e fuso horário.',
  'onboard.choosePlanHint':
    'Você pode mudar ou cancelar a qualquer momento. Todos os planos incluem compatibilidades semanais no seu e-mail.',
  'onboard.targetJobTitle': 'Cargo desejado',
  'onboard.targetJobTitlePlaceholder': 'ex. Engenheiro de Software Sênior',
  'onboard.experienceLevel': 'Nível de experiência',
  'onboard.entry': 'Iniciante (0–2 anos)',
  'onboard.junior': 'Júnior (2–4 anos)',
  'onboard.mid': 'Pleno (4–6 anos)',
  'onboard.senior': 'Sênior (6–10 anos)',
  'onboard.lead': 'Líder (10+ anos)',
  'onboard.executive': 'Executivo',
  'onboard.jobSearchStatus': 'Status da busca',
  'onboard.activelyLooking': 'Buscando ativamente',
  'onboard.openToOffers': 'Aberto a propostas',
  'onboard.casuallyBrowsing': 'Explorando sem pressa',
  'onboard.extraInfo': 'Informações adicionais',
  'onboard.extraInfoPlaceholder':
    'Qualquer outra coisa que queira nos contar — certificações, situação de visto, período de aviso prévio, etc.',
  'onboard.salaryPlaceholder': 'Valor anual',
  'onboard.targetSalary': 'Salário desejado',
  'onboard.uploadCV': 'Envie seu currículo',
  'onboard.chooseFile': 'Escolher arquivo',
  'onboard.cvFormats': 'PDF, DOCX, RTF ou TXT · até 10 MB',
  'onboard.readyToUpload': 'pronto para enviar',
  'onboard.remove': 'Remover',
  'onboard.cvPrivacy':
    'Seu currículo é usado para encontrar vagas relevantes. Ele nunca é compartilhado com empregadores sem a sua ação.',
  'onboard.regions': 'Regiões em que você pode trabalhar',
  'onboard.anywhere': 'Qualquer lugar',
  'onboard.africa': 'África',
  'onboard.europe': 'Europa',
  'onboard.northAmerica': 'América do Norte',
  'onboard.southAmerica': 'América do Sul',
  'onboard.asia': 'Ásia',
  'onboard.oceania': 'Oceania',
  'onboard.timezones': 'Fusos horários preferidos',
  'onboard.languages': 'Idiomas em que você trabalha',
  'onboard.jobType': 'Tipo de vaga',
  'onboard.fullTime': 'Tempo integral',
  'onboard.partTime': 'Meio período',
  'onboard.contract': 'Contrato',
  'onboard.freelance': 'Freelancer',
  'onboard.internship': 'Estágio',
  'onboard.country': 'País',
  'onboard.countryPlaceholder': 'ex. Brasil',
  'onboard.planUpgradeHint':
    'Você pode mudar ou cancelar a qualquer momento. Todos os planos incluem compatibilidades semanais no seu e-mail.',
  'onboard.matchesPerWeek': 'Até {count} compatibilidades por semana',
  'onboard.includesAgent': 'Inclui um agente dedicado',
  'onboard.agreeTermsLabel': 'Eu concordo com os',
  'onboard.agreeTermsAnd': 'e',
  'onboard.paymentRedirectHint':
    'Você será redirecionado ao nosso parceiro de pagamento para concluir a assinatura. Cancele a qualquer momento pelo seu painel.',
  'onboard.continueToPayment': 'Continuar para o pagamento',
  'onboard.continue': 'Continuar',
  'onboard.back': 'Voltar',
  'onboard.submitting': 'Enviando…',
  'onboard.openingSignIn': 'Abrindo login…',
  'onboard.draftSaveWarning':
    'Não foi possível salvar seu progresso. Suas respostas ainda estão aqui; tentaremos novamente no próximo passo.',
  'onboard.validationJobTitle': 'Informe um cargo desejado',
  'onboard.validationCVOrInfo': 'Envie um currículo ou conte-nos sobre você',
  'onboard.validationCV': 'Envie seu currículo para continuar',
  'onboard.validationCVSize': 'O currículo deve ter no máximo 10 MB',
  'onboard.validationCVFormat': 'Envie um arquivo PDF, DOCX, RTF ou TXT',
  'onboard.validationRegion': 'Selecione pelo menos uma região',
  'onboard.validationCountry': 'Informe seu país',
  'onboard.validationLanguage': 'Selecione pelo menos um idioma',
  'onboard.validationJobType': 'Selecione pelo menos um tipo de vaga',
  'onboard.validationTerms': 'Aceite os Termos antes de finalizar',
  'onboard.cvDropHere': 'Solte seu currículo aqui',
  'onboard.cvDragPrompt': 'Arraste e solte seu currículo aqui, ou clique para procurar',
  'onboard.skipPreferences': 'Pular por agora — vou configurar depois',
  'onboard.skipPreferencesHint': 'Você sempre pode configurar isso no seu painel',
  'onboard.features': 'recursos',
  'onboard.showLess': 'Mostrar menos',

  'dash.title': 'Seu painel',
  'dash.setupIncomplete': 'Configuração incompleta',
  'dash.finishPayment': 'Conclua o pagamento para ativar o matching.',
  'dash.browseJobs': 'Explorar vagas',
  'dash.changePlan': 'Alterar plano',
  'dash.viewPlans': 'Ver planos',
  'dash.actionNeeded': 'Ação necessária',
  'dash.choosePlan': 'Escolha um plano',
  'dash.editPreferences': 'Editar preferências',
  'dash.paymentReceived': 'Pagamento recebido — sua assinatura está ativa.',
  'dash.paymentFailed': 'O pagamento não foi concluído.',
  'dash.paymentWaiting':
    'Aguardando confirmação do seu provedor de pagamento — isso geralmente leva menos de um minuto.',
  'dash.matchPreferences': 'Preferências de matching',
  'dash.matchPreferencesHint':
    'Ative os tipos de oportunidades que deseja receber. Só executaremos os matchers para os tipos que você configurou.',
  'dash.saving': 'Salvando…',
  'dash.preferencesSaved': 'Preferências salvas.',
  'dash.preferencesFailed': 'Não foi possível salvar as preferências.',
  'dash.billing': 'Faturamento',
  'dash.active': 'Ativo',
  'dash.renewsOn': 'Renova em',
  'dash.changePlanOrCancel': 'Alterar plano ou cancelar →',
  'dash.signInTitle': 'Entre para continuar',
  'dash.signInHint': 'Seu painel mostra seu plano, compatibilidades e vagas salvas.',
  'dash.yourAgent': 'Seu agente',
  'dash.agentHint':
    'Seu recrutador pessoal durante toda a sua busca. Entre em contato a qualquer momento e espere uma resposta no mesmo dia.',
  'dash.emailAgent': 'E-mail',
  'dash.scheduleCall': 'Agendar uma conversa 1:1',
  'dash.paymentPastDue': 'Seu último pagamento não foi processado',
  'dash.updatePayment': 'Atualize seus dados de pagamento para retomar o matching.',
  'dash.subCancelled': 'Sua assinatura está cancelada',
  'dash.reactivateHint': 'Reative a qualquer momento para voltar a receber compatibilidades.',
  'dash.finishSetup': 'Conclua a configuração do seu plano {plan}',
  'dash.matchingHint':
    'Só executaremos nosso motor de matching no seu CV quando um plano estiver ativo. Leva dois minutos.',
  'dash.openingPayment': 'Abrindo pagamento…',
  'dash.payPerMonth': 'Pagar',
  'dash.perMonth': '/mês',
  'dash.welcomeTitle': 'Bem-vindo ao Stawi Opportunities!',
  'dash.welcomeBody':
    'Conectamos você às melhores oportunidades em toda a África. Comece completando seu perfil e definindo suas preferências.',
  'dash.welcomeDismiss': 'Dispensar',
  'dash.welcomeTour': 'Faça um tour rápido →',
  'dash.statusActive': 'Ativo',
  'dash.statusPastDue': 'Vencido',
  'dash.statusCancelled': 'Cancelado',
  'dash.emptyFeedTitle': 'Ainda não há oportunidades',
  'dash.emptyFeedHint': 'Conclua estas etapas para começar a receber correspondências:',
  'dash.emptyFeedCompleteProfile': 'Complete seu perfil',
  'dash.emptyFeedSetPreferences': 'Defina suas preferências',
  'dash.emptyFeedBrowseAll': 'Explore todas as oportunidades',
  'dash.celebrationTitle': 'Tudo pronto!',
  'dash.celebrationBody': 'Aqui está o que vem a seguir:',
  'dash.celebrationStep1': 'Estamos agora combinando você com oportunidades',
  'dash.celebrationStep2': 'Você receberá notificações por e-mail de novas correspondências',
  'dash.celebrationStep3': 'Suas primeiras correspondências aparecerão em 24 horas',
  'dash.celebrationDismiss': 'Entendi, mostre-me meu painel',
  'onboard.profileSaved': 'Perfil salvo!',

  'footer.jobSeekers': 'Candidatos',
  'footer.browseJobs': 'Explorar vagas',
  'footer.categories': 'Categorias',
  'footer.advancedSearch': 'Busca avançada',
  'footer.company': 'Empresa',
  'footer.about': 'Sobre',
  'footer.faq': 'FAQ',
  'footer.pricing': 'Planos',
  'footer.contact': 'Contato',
  'footer.legal': 'Jurídico',
  'footer.termsOfService': 'Termos de serviço',
  'footer.privacyPolicy': 'Política de privacidade',
  'footer.privacy': 'Privacidade',
  'footer.terms': 'Termos',
  'footer.rights': 'Todos os direitos reservados.',
  'footer.madeBy': 'Um produto de',
  'footer.explore': 'Explorar',
  'footer.jobs': 'Vagas',
  'footer.scholarships': 'Bolsas',
  'footer.tenders': 'Licitações',
  'footer.deals': 'Ofertas',
  'footer.funding': 'Financiamento',
};

// ---------------------------------------------------------------------------
// Japanese
// ---------------------------------------------------------------------------
const ja: Strings = {
  'nav.jobs': '求人',
  'nav.findJobs': '求人',
  'nav.allJobs': 'すべての求人',
  'nav.search': '詳細検索',
  'nav.about': '会社概要',
  'nav.faq': 'FAQ',
  'nav.pricing': '料金',
  'nav.signIn': 'ログイン',
  'nav.language': '言語',
  'nav.categoriesHint': '求人が登録されるとカテゴリが表示されます。',

  'cta.applyNow': '応募する',
  'cta.saveJob': '保存',
  'cta.loadMore': 'もっと見る',
  'cta.subscribe': '登録する',
  'cta.redeemNow': '今すぐ利用',
  'cta.submitBid': '入札する',
  'cta.share': '共有',
  'cta.copyLink': 'リンクをコピー',
  'cta.browseAll': 'すべてを見る',
  'cta.tryAgain': '再試行',
  'cta.apply': '応募',
  'cta.save': '保存',
  'cta.saved': '保存済み',
  'cta.cancel': 'キャンセル',
  'cta.close': '閉じる',
  'cta.dismiss': '非表示',
  'cta.retry': '再試行',
  'cta.twoMinutes': '設定はたった2分。',
  'cta.twoMinutesHint': '履歴書をアップロードし、希望を伝えるだけ。あとはお任せください。',
  'cta.getStarted': '始める',

  'job.postedOn': '掲載日',
  'job.remote': 'リモート',
  'job.employmentType': '雇用形態',
  'job.seniority': '経験レベル',
  'job.salary': '給与',
  'job.skillsRequired': '必須スキル',
  'job.skillsNiceToHave': '歓迎スキル',
  'job.translatedNotice': '元の求人情報から自動翻訳されました。',

  'deadline.closes': '締切',
  'deadline.expires': '有効期限',
  'deadline.applyBy': '応募期限',

  'expired.scholarship': 'この奨学金の募集は終了しました。',
  'expired.tender': 'この入札の受付期間は終了しました。',
  'expired.deal': 'このディールは終了しました。',
  'expired.funding': 'この助成金の募集は終了しました。',
  'expired.job': 'この求人の募集は終了しました。',

  'error.notFound': 'が見つかりません',
  'error.listingRemoved': 'この掲載は削除されたか、有効期限が切れています。',
  'error.somethingWrong': 'エラーが発生しました',
  'error.couldNotLoad': 'この掲載を読み込めませんでした。',
  'error.categoryLoad': 'このカテゴリを読み込めませんでした。',
  'error.submitFlag': '報告を送信できませんでした：',

  'kind.job': '求人',
  'kind.scholarship': '奨学金',
  'kind.tender': '入札案件',
  'kind.deal': 'セール',
  'kind.funding': '助成金',

  'common.home': 'ホーム',
  'common.categories': 'カテゴリ',
  'common.jobs': '件',
  'common.loading': '読み込み中…',
  'common.featured': '注目',

  'scholarship.minGpa': '最低GPA',
  'scholarship.eligibleNationalities': '対象国籍',

  'tender.budget': '予算',
  'tender.bidderEligibility': '入札者資格',
  'tender.submissionMethod': '提出方法',

  'deal.percentOff': '%オフ',
  'deal.couponCode': 'クーポンコード',
  'deal.redeemableIn': '利用可能地域',

  'funding.grant': '助成金',
  'funding.orgEligibility': '組織の応募資格',
  'funding.targetRegions': '対象地域',

  'search.placeholder': '職種・企業・スキルで検索…',
  'search.noResults': '該当する求人が見つかりませんでした。',
  'search.filters': 'フィルター',
  'search.clearAll': 'すべてクリア',
  'search.clear': 'クリア',
  'search.showResults': '結果を表示',
  'search.category': 'カテゴリ',
  'search.remote': 'リモート',
  'search.hybrid': 'ハイブリッド',
  'search.onSite': 'オンサイト',
  'search.employmentType': '雇用形態',
  'search.seniority': '経験レベル',
  'search.country': '国',
  'search.searchPlaceholder': 'タイトル、スキル、企業名で検索…',
  'search.searchButton': '検索',
  'search.searchJobs': '求人を検索',
  'search.sort': '並び替え',
  'search.sortRelevance': '関連度',
  'search.sortRecent': '新しい順',
  'search.sortQuality': '品質順',
  'search.sortSalary': '給与：高い順',
  'search.uncategorised': '（未分類）',

  'flag.trigger': 'この掲載を報告',
  'flag.title': 'この掲載を報告',
  'flag.scam': '詐欺またはフィッシング',
  'flag.expired': '期限切れまたは募集終了',
  'flag.duplicate': '重複した掲載',
  'flag.spam': 'スパムまたは低品質',
  'flag.other': 'その他',
  'flag.thankYou': 'ありがとうございます。報告は記録され、モデレーターが確認します。',
  'flag.reason': '理由',
  'flag.details': '詳細',
  'flag.detailsPlaceholder': 'この掲載のどこが問題ですか？',
  'flag.alreadyFlagged':
    'この掲載は既に報告済みです。サイトの品質維持にご協力いただきありがとうございます。',
  'flag.signInRequired': '掲載を報告するにはログインが必要です。',
  'flag.submitting': '送信中…',
  'flag.submitButton': '報告を送信',

  'stats.views': '閲覧',
  'stats.applies': '応募',

  'locale.showingJobs': '{country}の求人{langs}を表示中。',

  'category.browseByCategory': 'カテゴリで探す',
  'category.noCategories': 'カテゴリはまだありません。',
  'category.uncategorised': '未分類',
  'category.notFound': 'カテゴリが見つかりません',
  'category.notFoundMessage': 'そのカテゴリは（まだ）存在しません。',
  'category.backToAll': 'すべてのカテゴリに戻る',
  'category.latestRoles': '{category}の最新求人 — カテゴリでフィルタリングし、日付順に表示。',
  'category.noRolesOpen': '現在{category}の募集はありません。',
  'category.browseAllJobs': 'すべての求人を見る →',

  'status.applied': '応募済み',
  'status.responded': '返信あり',
  'status.interview': '面接予定',
  'status.offer': '内定',
  'status.rejected': '不採用',
  'status.hired': '採用',
  'card.match': '%マッチ',
  'card.new': '新着',

  'cascade.yourPreferences': 'あなたの希望条件',
  'cascade.outside': '{country}以外',
  'cascade.worldwide': '世界中',
  'cascade.loadingMore': '読み込み中…',

  'feed.all': 'すべて',
  'feed.matches': 'マッチ',
  'feed.starred': 'お気に入り',
  'feed.applied': '応募済み',
  'feed.empty': 'まだ表示するものがありません。',
  'feed.tryAllFilter': '「すべて」フィルターをお試しください。',
  'feed.loadError': '求人情報を読み込めませんでした。',
  'feed.opportunities': '機会',

  'onboard.step': 'ステップ',
  'onboard.of': '/',
  'onboard.aboutYou': 'あなたについて',
  'onboard.yourPreferences': '希望条件',
  'onboard.choosePlan': 'プランを選択',
  'onboard.aboutYouHint': 'お探しの内容を教えてください。最も関連性の高い求人を表示します。',
  'onboard.preferencesHint': 'お住まいの地域やタイムゾーンに合わない求人を除外します。',
  'onboard.choosePlanHint':
    'いつでもアップグレードまたは解約できます。すべてのプランに週次のマッチング配信が含まれます。',
  'onboard.targetJobTitle': '希望職種',
  'onboard.targetJobTitlePlaceholder': '例: シニアソフトウェアエンジニア',
  'onboard.experienceLevel': '経験レベル',
  'onboard.entry': '新卒・初級（0〜2年）',
  'onboard.junior': 'ジュニア（2〜4年）',
  'onboard.mid': 'ミドル（4〜6年）',
  'onboard.senior': 'シニア（6〜10年）',
  'onboard.lead': 'リード（10年以上）',
  'onboard.executive': 'エグゼクティブ',
  'onboard.jobSearchStatus': '就職活動の状況',
  'onboard.activelyLooking': '積極的に探している',
  'onboard.openToOffers': 'オファーに前向き',
  'onboard.casuallyBrowsing': '気軽に閲覧中',
  'onboard.extraInfo': '補足情報',
  'onboard.extraInfoPlaceholder':
    'その他お知らせいただきたいこと — 資格、ビザの状況、退職予定時期など。',
  'onboard.salaryPlaceholder': '年収',
  'onboard.targetSalary': '希望年収',
  'onboard.uploadCV': '履歴書をアップロード',
  'onboard.chooseFile': 'ファイルを選択',
  'onboard.cvFormats': 'PDF、DOCX、RTF、TXT · 10 MBまで',
  'onboard.readyToUpload': 'アップロード準備完了',
  'onboard.remove': '削除',
  'onboard.cvPrivacy':
    '履歴書は適切な求人とのマッチングに使用されます。あなたの操作なしに雇用主と共有されることはありません。',
  'onboard.regions': '勤務可能な地域',
  'onboard.anywhere': 'どこでも',
  'onboard.africa': 'アフリカ',
  'onboard.europe': 'ヨーロッパ',
  'onboard.northAmerica': '北米',
  'onboard.southAmerica': '南米',
  'onboard.asia': 'アジア',
  'onboard.oceania': 'オセアニア',
  'onboard.timezones': '希望タイムゾーン',
  'onboard.languages': '業務で使用する言語',
  'onboard.jobType': '雇用形態',
  'onboard.fullTime': '正社員',
  'onboard.partTime': 'パートタイム',
  'onboard.contract': '契約社員',
  'onboard.freelance': 'フリーランス',
  'onboard.internship': 'インターンシップ',
  'onboard.country': '国',
  'onboard.countryPlaceholder': '例: 日本',
  'onboard.planUpgradeHint':
    'いつでもアップグレードまたは解約できます。すべてのプランに週次のマッチング配信が含まれます。',
  'onboard.matchesPerWeek': '週最大 {count} 件のマッチング',
  'onboard.includesAgent': '専任エージェント付き',
  'onboard.agreeTermsLabel': '以下に同意します：',
  'onboard.agreeTermsAnd': 'および',
  'onboard.paymentRedirectHint':
    'サブスクリプションの完了のため、決済パートナーのページに移動します。ダッシュボードからいつでも解約できます。',
  'onboard.continueToPayment': '支払いに進む',
  'onboard.continue': '次へ',
  'onboard.back': '戻る',
  'onboard.submitting': '送信中…',
  'onboard.openingSignIn': 'ログイン画面を開いています…',
  'onboard.draftSaveWarning':
    '進行状況を保存できませんでした。入力内容はそのまま残っています。次のステップで再試行します。',
  'onboard.validationJobTitle': '希望職種を入力してください',
  'onboard.validationCVOrInfo': '履歴書を提出するか、自己紹介を記入してください',
  'onboard.validationCV': '続行するには履歴書をアップロードしてください',
  'onboard.validationCVSize': '履歴書は10 MB以下にしてください',
  'onboard.validationCVFormat': 'PDF、DOCX、RTF、またはTXTファイルをアップロードしてください',
  'onboard.validationRegion': '少なくとも1つの地域を選択してください',
  'onboard.validationCountry': '国を入力してください',
  'onboard.validationLanguage': '少なくとも1つの言語を選択してください',
  'onboard.validationJobType': '少なくとも1つの雇用形態を選択してください',
  'onboard.validationTerms': '完了する前に利用規約に同意してください',
  'onboard.cvDropHere': 'ここに履歴書をドロップ',
  'onboard.cvDragPrompt': '履歴書をドラッグ＆ドロップするか、クリックして選択',
  'onboard.skipPreferences': 'スキップ — 後で設定します',
  'onboard.skipPreferencesHint': 'ダッシュボードからいつでも設定できます',
  'onboard.features': '機能',
  'onboard.showLess': '表示を減らす',

  'dash.title': 'ダッシュボード',
  'dash.setupIncomplete': '設定が未完了です',
  'dash.finishPayment': 'お支払いを完了してマッチングを有効にしてください。',
  'dash.browseJobs': '求人を閲覧',
  'dash.changePlan': 'プランを変更',
  'dash.viewPlans': 'プランを見る',
  'dash.actionNeeded': '対応が必要です',
  'dash.choosePlan': 'プランを選択',
  'dash.editPreferences': '希望条件を編集',
  'dash.paymentReceived': 'お支払いを確認しました — サブスクリプションが有効になりました。',
  'dash.paymentFailed': 'お支払いが完了しませんでした。',
  'dash.paymentWaiting': '決済プロバイダーの確認を待っています — 通常1分以内に完了します。',
  'dash.matchPreferences': 'マッチング設定',
  'dash.matchPreferencesHint':
    '受け取りたい求人の種類を選択してください。設定された種類のみマッチングを実行します。',
  'dash.saving': '保存中…',
  'dash.preferencesSaved': '設定を保存しました。',
  'dash.preferencesFailed': '設定を保存できませんでした。',
  'dash.billing': 'お支払い',
  'dash.active': '有効',
  'dash.renewsOn': '更新日',
  'dash.changePlanOrCancel': 'プラン変更または解約 →',
  'dash.signInTitle': 'ログインしてください',
  'dash.signInHint': 'ダッシュボードではプラン、マッチ結果、保存した求人を確認できます。',
  'dash.yourAgent': 'あなたの担当エージェント',
  'dash.agentHint':
    '就職活動中ずっとサポートする専任リクルーターです。いつでもご連絡ください。当日中にお返事します。',
  'dash.emailAgent': 'メール',
  'dash.scheduleCall': '1対1の面談を予約',
  'dash.paymentPastDue': '前回のお支払いが処理されませんでした',
  'dash.updatePayment': 'お支払い情報を更新してマッチングを再開してください。',
  'dash.subCancelled': 'サブスクリプションは解約済みです',
  'dash.reactivateHint': 'いつでも再開してマッチングを再び受け取れます。',
  'dash.finishSetup': '{plan}プランの設定を完了してください',
  'dash.matchingHint':
    'プランが有効になってから、あなたの履歴書でマッチングエンジンを実行します。所要時間は2分です。',
  'dash.openingPayment': '決済画面を開いています…',
  'dash.payPerMonth': '支払う',
  'dash.perMonth': '/月',
  'dash.welcomeTitle': 'Stawi Opportunitiesへようこそ！',
  'dash.welcomeBody':
    'アフリカ全土の最適な機会をマッチングします。プロフィールを完了し、希望条件を設定してください。',
  'dash.welcomeDismiss': '閉じる',
  'dash.welcomeTour': 'クイックツアー →',
  'dash.statusActive': '有効',
  'dash.statusPastDue': '支払い期限切れ',
  'dash.statusCancelled': '解約済み',
  'dash.emptyFeedTitle': 'まだ機会はありません',
  'dash.emptyFeedHint': '以下の手順を完了してマッチングを受け取りましょう:',
  'dash.emptyFeedCompleteProfile': 'プロフィールを完了する',
  'dash.emptyFeedSetPreferences': '希望条件を設定する',
  'dash.emptyFeedBrowseAll': 'すべての機会を閲覧する',
  'dash.celebrationTitle': '準備完了！',
  'dash.celebrationBody': '次のステップ:',
  'dash.celebrationStep1': 'あなたに合った機会をマッチングしています',
  'dash.celebrationStep2': '新しいマッチングのメール通知を受け取ります',
  'dash.celebrationStep3': '最初のマッチングは24時間以内に表示されます',
  'dash.celebrationDismiss': 'わかりました、ダッシュボードを表示',
  'onboard.profileSaved': 'プロフィールを保存しました！',

  'footer.jobSeekers': '求職者の方へ',
  'footer.browseJobs': '求人を閲覧',
  'footer.categories': 'カテゴリ',
  'footer.advancedSearch': '詳細検索',
  'footer.company': '会社情報',
  'footer.about': '会社概要',
  'footer.faq': 'FAQ',
  'footer.pricing': '料金',
  'footer.contact': 'お問い合わせ',
  'footer.legal': '法的情報',
  'footer.termsOfService': '利用規約',
  'footer.privacyPolicy': 'プライバシーポリシー',
  'footer.privacy': 'プライバシー',
  'footer.terms': '利用規約',
  'footer.rights': 'All rights reserved.',
  'footer.madeBy': '提供元',
  'footer.explore': '探す',
  'footer.jobs': '求人',
  'footer.scholarships': '奨学金',
  'footer.tenders': '入札',
  'footer.deals': 'お得',
  'footer.funding': '資金調達',
};

// ---------------------------------------------------------------------------
// Arabic
// ---------------------------------------------------------------------------
const ar: Strings = {
  'nav.jobs': 'وظائف',
  'nav.findJobs': 'الوظائف',
  'nav.allJobs': 'كل الوظائف',
  'nav.search': 'بحث متقدم',
  'nav.about': 'من نحن',
  'nav.faq': 'الأسئلة الشائعة',
  'nav.pricing': 'الأسعار',
  'nav.signIn': 'تسجيل الدخول',
  'nav.language': 'اللغة',
  'nav.categoriesHint': 'ستظهر الفئات بمجرد فهرسة الوظائف.',

  'cta.applyNow': 'قدّم الآن',
  'cta.saveJob': 'حفظ الوظيفة',
  'cta.loadMore': 'عرض المزيد',
  'cta.subscribe': 'اشترك',
  'cta.redeemNow': 'استرد الآن',
  'cta.submitBid': 'تقديم عرض',
  'cta.share': 'مشاركة',
  'cta.copyLink': 'نسخ الرابط',
  'cta.browseAll': 'تصفح الكل',
  'cta.tryAgain': 'حاول مجدداً',
  'cta.apply': 'تقديم',
  'cta.save': 'حفظ',
  'cta.saved': 'محفوظ',
  'cta.cancel': 'إلغاء',
  'cta.close': 'إغلاق',
  'cta.dismiss': 'تجاهل',
  'cta.retry': 'إعادة المحاولة',
  'cta.twoMinutes': 'جاهز في دقيقتين.',
  'cta.twoMinutesHint': 'ارفع سيرتك الذاتية، أخبرنا بما تبحث عنه، ونحن نتولى الباقي.',
  'cta.getStarted': 'ابدأ الآن',

  'job.postedOn': 'نُشرت في',
  'job.remote': 'عن بُعد',
  'job.employmentType': 'نوع الوظيفة',
  'job.seniority': 'المستوى',
  'job.salary': 'الراتب',
  'job.skillsRequired': 'المهارات المطلوبة',
  'job.skillsNiceToHave': 'مهارات إضافية',
  'job.translatedNotice': 'تمت الترجمة تلقائيًا من الإعلان الأصلي.',

  'deadline.closes': 'يُغلق في',
  'deadline.expires': 'ينتهي في',
  'deadline.applyBy': 'التقديم قبل',

  'expired.scholarship': 'لم تعد هذه المنحة تقبل طلبات.',
  'expired.tender': 'انتهت فترة تقديم هذا المناقصة.',
  'expired.deal': 'انتهت صلاحية هذا العرض.',
  'expired.funding': 'تم إغلاق فرصة التمويل هذه.',
  'expired.job': 'لم تعد هذه الوظيفة تقبل طلبات.',

  'error.notFound': 'غير موجود',
  'error.listingRemoved': 'تمت إزالة هذا الإعلان أو انتهت صلاحيته.',
  'error.somethingWrong': 'حدث خطأ ما',
  'error.couldNotLoad': 'تعذر تحميل هذا الإعلان حالياً.',
  'error.categoryLoad': 'تعذر تحميل هذه الفئة.',
  'error.submitFlag': 'تعذر إرسال البلاغ:',

  'kind.job': 'وظائف',
  'kind.scholarship': 'منح دراسية',
  'kind.tender': 'مناقصات',
  'kind.deal': 'عروض',
  'kind.funding': 'تمويل',

  'common.home': 'الرئيسية',
  'common.categories': 'الفئات',
  'common.jobs': 'وظائف',
  'common.loading': 'جارٍ التحميل…',
  'common.featured': 'مميز',

  'scholarship.minGpa': 'الحد الأدنى للمعدل',
  'scholarship.eligibleNationalities': 'الجنسيات المؤهلة',

  'tender.budget': 'الميزانية',
  'tender.bidderEligibility': 'أهلية المتقدم',
  'tender.submissionMethod': 'طريقة التقديم',

  'deal.percentOff': '% خصم',
  'deal.couponCode': 'رمز القسيمة',
  'deal.redeemableIn': 'قابل للاسترداد في',

  'funding.grant': 'المنحة',
  'funding.orgEligibility': 'أهلية المنظمة',
  'funding.targetRegions': 'المناطق المستهدفة',

  'search.placeholder': 'ابحث عن وظائف وشركات ومهارات…',
  'search.noResults': 'لا توجد وظائف مطابقة.',
  'search.filters': 'التصفية',
  'search.clearAll': 'مسح الكل',
  'search.clear': 'مسح',
  'search.showResults': 'عرض النتائج',
  'search.category': 'الفئة',
  'search.remote': 'عن بُعد',
  'search.hybrid': 'هجين',
  'search.onSite': 'في الموقع',
  'search.employmentType': 'نوع الوظيفة',
  'search.seniority': 'المستوى',
  'search.country': 'البلد',
  'search.searchPlaceholder': 'ابحث بالمسمى أو المهارة أو الشركة…',
  'search.searchButton': 'بحث',
  'search.searchJobs': 'البحث عن وظائف',
  'search.sort': 'ترتيب',
  'search.sortRelevance': 'الأكثر صلة',
  'search.sortRecent': 'الأحدث',
  'search.sortQuality': 'الأعلى جودة',
  'search.sortSalary': 'الراتب: من الأعلى',
  'search.uncategorised': '(بدون تصنيف)',

  'flag.trigger': 'الإبلاغ عن هذا الإعلان',
  'flag.title': 'الإبلاغ عن هذا الإعلان',
  'flag.scam': 'احتيال أو تصيد',
  'flag.expired': 'منتهي أو مشغول',
  'flag.duplicate': 'إعلان مكرر',
  'flag.spam': 'بريد عشوائي أو جودة منخفضة',
  'flag.other': 'أخرى',
  'flag.thankYou': 'شكراً — تم تسجيل بلاغك وسيتم مراجعته من قبل فريق الإشراف.',
  'flag.reason': 'السبب',
  'flag.details': 'تفاصيل',
  'flag.detailsPlaceholder': 'ما المشكلة في هذا الإعلان؟',
  'flag.alreadyFlagged':
    'لقد أبلغت عن هذا الإعلان سابقاً — شكراً لمساعدتك في الحفاظ على جودة الموقع.',
  'flag.signInRequired': 'يجب تسجيل الدخول للإبلاغ عن إعلان.',
  'flag.submitting': 'جارٍ الإرسال…',
  'flag.submitButton': 'إرسال البلاغ',

  'stats.views': 'مشاهدة',
  'stats.applies': 'تقديم',

  'locale.showingJobs': 'عرض وظائف {country}{langs}.',

  'category.browseByCategory': 'تصفح حسب الفئة',
  'category.noCategories': 'لا توجد فئات بعد.',
  'category.uncategorised': 'بدون تصنيف',
  'category.notFound': 'الفئة غير موجودة',
  'category.notFoundMessage': 'هذه الفئة غير موجودة (حتى الآن).',
  'category.backToAll': 'العودة لجميع الفئات',
  'category.latestRoles': 'أحدث الوظائف في {category} — مُصفاة حسب الفئة ومرتبة حسب التاريخ.',
  'category.noRolesOpen': 'لا توجد وظائف مفتوحة في {category} حالياً.',
  'category.browseAllJobs': 'تصفح جميع الوظائف ←',

  'status.applied': 'تم التقديم',
  'status.responded': 'تم الرد',
  'status.interview': 'مقابلة مجدولة',
  'status.offer': 'عرض مستلم',
  'status.rejected': 'مرفوض',
  'status.hired': 'تم التوظيف',
  'card.match': '% تطابق',
  'card.new': 'جديد',

  'cascade.yourPreferences': 'تفضيلاتك',
  'cascade.outside': 'خارج {country}',
  'cascade.worldwide': 'عالمي',
  'cascade.loadingMore': 'جارٍ تحميل المزيد…',

  'feed.all': 'الكل',
  'feed.matches': 'المتطابقة',
  'feed.starred': 'المفضلة',
  'feed.applied': 'المُقدَّم عليها',
  'feed.empty': 'لا يوجد شيء لعرضه بعد.',
  'feed.tryAllFilter': 'جرّب فلتر "الكل".',
  'feed.loadError': 'تعذر تحميل فرصك.',
  'feed.opportunities': 'فرصة',

  'onboard.step': 'الخطوة',
  'onboard.of': 'من',
  'onboard.aboutYou': 'عنك',
  'onboard.yourPreferences': 'تفضيلاتك',
  'onboard.choosePlan': 'اختر خطتك',
  'onboard.aboutYouHint': 'أخبرنا بما تبحث عنه حتى نعرض لك الوظائف الأكثر ملاءمة.',
  'onboard.preferencesHint': 'سنستبعد الوظائف التي لا تتوافق مع موقعك ومنطقتك الزمنية.',
  'onboard.choosePlanHint':
    'يمكنك الترقية أو الإلغاء في أي وقت. جميع الخطط تشمل مطابقات أسبوعية إلى بريدك.',
  'onboard.targetJobTitle': 'المسمى الوظيفي المطلوب',
  'onboard.targetJobTitlePlaceholder': 'مثال: مهندس برمجيات أول',
  'onboard.experienceLevel': 'مستوى الخبرة',
  'onboard.entry': 'مبتدئ (0–2 سنوات)',
  'onboard.junior': 'مبتدئ متقدم (2–4 سنوات)',
  'onboard.mid': 'متوسط (4–6 سنوات)',
  'onboard.senior': 'خبير (6–10 سنوات)',
  'onboard.lead': 'قيادي (10+ سنوات)',
  'onboard.executive': 'تنفيذي',
  'onboard.jobSearchStatus': 'حالة البحث عن عمل',
  'onboard.activelyLooking': 'أبحث بنشاط',
  'onboard.openToOffers': 'منفتح على العروض',
  'onboard.casuallyBrowsing': 'أتصفح بدون عجلة',
  'onboard.extraInfo': 'معلومات إضافية',
  'onboard.extraInfoPlaceholder':
    'أي شيء آخر تريد إطلاعنا عليه — شهادات، حالة التأشيرة، فترة الإشعار، إلخ.',
  'onboard.salaryPlaceholder': 'المبلغ السنوي',
  'onboard.targetSalary': 'الراتب المستهدف',
  'onboard.uploadCV': 'ارفع سيرتك الذاتية',
  'onboard.chooseFile': 'اختر ملفاً',
  'onboard.cvFormats': 'PDF أو DOCX أو RTF أو TXT · حتى 10 ميجابايت',
  'onboard.readyToUpload': 'جاهز للرفع',
  'onboard.remove': 'إزالة',
  'onboard.cvPrivacy':
    'تُستخدم سيرتك الذاتية لمطابقتك بالوظائف المناسبة. لن تتم مشاركتها مع أصحاب العمل دون إجراء منك.',
  'onboard.regions': 'المناطق التي يمكنك العمل فيها',
  'onboard.anywhere': 'أي مكان',
  'onboard.africa': 'أفريقيا',
  'onboard.europe': 'أوروبا',
  'onboard.northAmerica': 'أمريكا الشمالية',
  'onboard.southAmerica': 'أمريكا الجنوبية',
  'onboard.asia': 'آسيا',
  'onboard.oceania': 'أوقيانوسيا',
  'onboard.timezones': 'المناطق الزمنية المفضلة',
  'onboard.languages': 'اللغات التي تعمل بها',
  'onboard.jobType': 'نوع الوظيفة',
  'onboard.fullTime': 'دوام كامل',
  'onboard.partTime': 'دوام جزئي',
  'onboard.contract': 'عقد محدد',
  'onboard.freelance': 'عمل حر',
  'onboard.internship': 'تدريب',
  'onboard.country': 'البلد',
  'onboard.countryPlaceholder': 'مثال: مصر',
  'onboard.planUpgradeHint':
    'يمكنك الترقية أو الإلغاء في أي وقت. جميع الخطط تشمل مطابقات أسبوعية إلى بريدك.',
  'onboard.matchesPerWeek': 'حتى {count} مطابقة أسبوعياً',
  'onboard.includesAgent': 'يتضمن وكيلاً مخصصاً',
  'onboard.agreeTermsLabel': 'أوافق على',
  'onboard.agreeTermsAnd': 'و',
  'onboard.paymentRedirectHint':
    'ستتم إعادة توجيهك إلى شريك الدفع لإتمام الاشتراك. يمكنك الإلغاء في أي وقت من لوحة التحكم.',
  'onboard.continueToPayment': 'المتابعة إلى الدفع',
  'onboard.continue': 'متابعة',
  'onboard.back': 'رجوع',
  'onboard.submitting': 'جارٍ الإرسال…',
  'onboard.openingSignIn': 'جارٍ فتح صفحة تسجيل الدخول…',
  'onboard.draftSaveWarning':
    'تعذر حفظ تقدمك. إجاباتك لا تزال هنا؛ سنحاول مجدداً في الخطوة التالية.',
  'onboard.validationJobTitle': 'أدخل المسمى الوظيفي المطلوب',
  'onboard.validationCVOrInfo': 'قدّم سيرة ذاتية أو أخبرنا عن نفسك',
  'onboard.validationCV': 'ارفع سيرتك الذاتية للمتابعة',
  'onboard.validationCVSize': 'يجب ألا يتجاوز حجم السيرة الذاتية 10 ميجابايت',
  'onboard.validationCVFormat': 'ارفع ملف PDF أو DOCX أو RTF أو TXT',
  'onboard.validationRegion': 'اختر منطقة واحدة على الأقل',
  'onboard.validationCountry': 'أدخل بلدك',
  'onboard.validationLanguage': 'اختر لغة واحدة على الأقل',
  'onboard.validationJobType': 'اختر نوع وظيفة واحداً على الأقل',
  'onboard.validationTerms': 'يرجى الموافقة على الشروط قبل الإنهاء',
  'onboard.cvDropHere': 'أسقط سيرتك الذاتية هنا',
  'onboard.cvDragPrompt': 'اسحب وأفلت سيرتك الذاتية هنا، أو انقر للتصفح',
  'onboard.skipPreferences': 'تخط الآن — سأضبط هذا لاحقًا',
  'onboard.skipPreferencesHint': 'يمكنك دائمًا تكوين هذه الإعدادات من لوحة التحكم',
  'onboard.features': 'ميزات',
  'onboard.showLess': 'عرض أقل',

  'dash.title': 'لوحة التحكم',
  'dash.setupIncomplete': 'الإعداد غير مكتمل',
  'dash.finishPayment': 'أكمل الدفع لتفعيل المطابقة.',
  'dash.browseJobs': 'تصفح الوظائف',
  'dash.changePlan': 'تغيير الخطة',
  'dash.viewPlans': 'عرض الخطط',
  'dash.actionNeeded': 'إجراء مطلوب',
  'dash.choosePlan': 'اختر خطة',
  'dash.editPreferences': 'تعديل التفضيلات',
  'dash.paymentReceived': 'تم استلام الدفعة — اشتراكك نشط الآن.',
  'dash.paymentFailed': 'لم يكتمل الدفع.',
  'dash.paymentWaiting': 'بانتظار تأكيد مزود الدفع — عادةً ما يستغرق أقل من دقيقة.',
  'dash.matchPreferences': 'تفضيلات المطابقة',
  'dash.matchPreferencesHint':
    'فعّل أنواع الفرص التي تريد مطابقتها. لن نشغّل المطابقة إلا للأنواع التي قمت بتهيئتها.',
  'dash.saving': 'جارٍ الحفظ…',
  'dash.preferencesSaved': 'تم حفظ التفضيلات.',
  'dash.preferencesFailed': 'تعذر حفظ التفضيلات.',
  'dash.billing': 'الفوترة',
  'dash.active': 'نشط',
  'dash.renewsOn': 'يُجدد في',
  'dash.changePlanOrCancel': 'تغيير الخطة أو الإلغاء ←',
  'dash.signInTitle': 'سجّل الدخول للمتابعة',
  'dash.signInHint': 'تعرض لوحة التحكم خطتك ونتائج المطابقة والوظائف المحفوظة.',
  'dash.yourAgent': 'وكيلك',
  'dash.agentHint':
    'مسؤول التوظيف الشخصي طوال فترة بحثك. تواصل معه في أي وقت وتوقع رداً في نفس اليوم.',
  'dash.emailAgent': 'البريد الإلكتروني',
  'dash.scheduleCall': 'حجز مكالمة فردية',
  'dash.paymentPastDue': 'لم تتم معالجة دفعتك الأخيرة',
  'dash.updatePayment': 'حدّث بيانات الدفع لاستئناف المطابقة.',
  'dash.subCancelled': 'اشتراكك ملغى',
  'dash.reactivateHint': 'أعد التفعيل في أي وقت لبدء استلام نتائج المطابقة مجدداً.',
  'dash.finishSetup': 'أكمل إعداد خطة {plan}',
  'dash.matchingHint':
    'لن نشغّل محرك المطابقة على سيرتك الذاتية إلا عند تفعيل خطة. يستغرق الأمر دقيقتين.',
  'dash.openingPayment': 'جارٍ فتح صفحة الدفع…',
  'dash.payPerMonth': 'ادفع',
  'dash.perMonth': '/شهر',
  'dash.welcomeTitle': 'مرحبًا بك في ستاوي للفرص!',
  'dash.welcomeBody':
    'نوفر لك أفضل الفرص في جميع أنحاء أفريقيا. ابدأ بإكمال ملفك الشخصي وتحديد تفضيلاتك.',
  'dash.welcomeDismiss': 'تجاهل',
  'dash.welcomeTour': 'جولة سريعة ←',
  'dash.statusActive': 'نشط',
  'dash.statusPastDue': 'متأخر',
  'dash.statusCancelled': 'ملغي',
  'dash.emptyFeedTitle': 'لا توجد فرص بعد',
  'dash.emptyFeedHint': 'أكمل هذه الخطوات لبدء تلقي المطابقات:',
  'dash.emptyFeedCompleteProfile': 'أكمل ملفك الشخصي',
  'dash.emptyFeedSetPreferences': 'حدد تفضيلاتك',
  'dash.emptyFeedBrowseAll': 'تصفح جميع الفرص',
  'dash.celebrationTitle': 'كل شيء جاهز!',
  'dash.celebrationBody': 'إليك ما يلي:',
  'dash.celebrationStep1': 'نقوم الآن بمطابقتك مع الفرص',
  'dash.celebrationStep2': 'ستتلقى إشعارات بريد إلكتروني للمطابقات الجديدة',
  'dash.celebrationStep3': 'ستظهر مطابقتك الأولى خلال 24 ساعة',
  'dash.celebrationDismiss': 'حسنًا، أظهر لي لوحة التحكم',
  'onboard.profileSaved': 'تم حفظ الملف الشخصي!',

  'footer.jobSeekers': 'الباحثون عن عمل',
  'footer.browseJobs': 'تصفح الوظائف',
  'footer.categories': 'الفئات',
  'footer.advancedSearch': 'بحث متقدم',
  'footer.company': 'الشركة',
  'footer.about': 'من نحن',
  'footer.faq': 'الأسئلة الشائعة',
  'footer.pricing': 'الأسعار',
  'footer.contact': 'اتصل بنا',
  'footer.legal': 'قانوني',
  'footer.termsOfService': 'شروط الخدمة',
  'footer.privacyPolicy': 'سياسة الخصوصية',
  'footer.privacy': 'الخصوصية',
  'footer.terms': 'الشروط',
  'footer.rights': 'جميع الحقوق محفوظة.',
  'footer.madeBy': 'منتج من',
  'footer.explore': 'استكشاف',
  'footer.jobs': 'وظائف',
  'footer.scholarships': 'منح دراسية',
  'footer.tenders': 'مناقصات',
  'footer.deals': 'صفقات',
  'footer.funding': 'تمويل',
};

// ---------------------------------------------------------------------------
// Chinese (Simplified)
// ---------------------------------------------------------------------------
const zh: Strings = {
  'nav.jobs': '职位',
  'nav.findJobs': '招聘',
  'nav.allJobs': '全部职位',
  'nav.search': '高级搜索',
  'nav.about': '关于',
  'nav.faq': '常见问题',
  'nav.pricing': '定价',
  'nav.signIn': '登录',
  'nav.language': '语言',
  'nav.categoriesHint': '职位入库后将显示分类。',

  'cta.applyNow': '立即申请',
  'cta.saveJob': '收藏职位',
  'cta.loadMore': '加载更多',
  'cta.subscribe': '订阅',
  'cta.redeemNow': '立即兑换',
  'cta.submitBid': '提交投标',
  'cta.share': '分享',
  'cta.copyLink': '复制链接',
  'cta.browseAll': '浏览全部',
  'cta.tryAgain': '重试',
  'cta.apply': '申请',
  'cta.save': '收藏',
  'cta.saved': '已收藏',
  'cta.cancel': '取消',
  'cta.close': '关闭',
  'cta.dismiss': '忽略',
  'cta.retry': '重试',
  'cta.twoMinutes': '两分钟即可完成设置。',
  'cta.twoMinutesHint': '上传简历，告诉我们您的求职意向，剩下的交给我们。',
  'cta.getStarted': '立即开始',

  'job.postedOn': '发布时间',
  'job.remote': '远程',
  'job.employmentType': '雇佣类型',
  'job.seniority': '经验要求',
  'job.salary': '薪资',
  'job.skillsRequired': '必备技能',
  'job.skillsNiceToHave': '加分项',
  'job.translatedNotice': '此内容由原始职位自动翻译。',

  'deadline.closes': '截止',
  'deadline.expires': '到期',
  'deadline.applyBy': '申请截止',

  'expired.scholarship': '该奖学金已停止接受申请。',
  'expired.tender': '该招标的提交窗口已关闭。',
  'expired.deal': '该优惠已过期。',
  'expired.funding': '该资助机会已关闭。',
  'expired.job': '该职位已停止接受申请。',

  'error.notFound': '未找到',
  'error.listingRemoved': '该信息已被删除或已过期。',
  'error.somethingWrong': '出了点问题',
  'error.couldNotLoad': '暂时无法加载该信息。',
  'error.categoryLoad': '无法加载该分类。',
  'error.submitFlag': '无法提交举报：',

  'kind.job': '职位',
  'kind.scholarship': '奖学金',
  'kind.tender': '招标',
  'kind.deal': '优惠',
  'kind.funding': '资助',

  'common.home': '首页',
  'common.categories': '分类',
  'common.jobs': '个职位',
  'common.loading': '加载中…',
  'common.featured': '推荐',

  'scholarship.minGpa': '最低GPA',
  'scholarship.eligibleNationalities': '符合条件的国籍',

  'tender.budget': '预算',
  'tender.bidderEligibility': '投标人资格',
  'tender.submissionMethod': '提交方式',

  'deal.percentOff': '折扣',
  'deal.couponCode': '优惠码',
  'deal.redeemableIn': '可兑换地区',

  'funding.grant': '资助',
  'funding.orgEligibility': '组织资格',
  'funding.targetRegions': '目标地区',

  'search.placeholder': '搜索职位、公司、技能…',
  'search.noResults': '没有找到匹配的职位。',
  'search.filters': '筛选',
  'search.clearAll': '清除全部',
  'search.clear': '清除',
  'search.showResults': '显示结果',
  'search.category': '分类',
  'search.remote': '远程',
  'search.hybrid': '混合',
  'search.onSite': '现场',
  'search.employmentType': '雇佣类型',
  'search.seniority': '经验要求',
  'search.country': '国家',
  'search.searchPlaceholder': '按职位、技能或公司搜索…',
  'search.searchButton': '搜索',
  'search.searchJobs': '搜索职位',
  'search.sort': '排序',
  'search.sortRelevance': '相关度',
  'search.sortRecent': '最新',
  'search.sortQuality': '最高质量',
  'search.sortSalary': '薪资：从高到低',
  'search.uncategorised': '（未分类）',

  'flag.trigger': '举报此信息',
  'flag.title': '举报此信息',
  'flag.scam': '诈骗或钓鱼',
  'flag.expired': '已过期或已招满',
  'flag.duplicate': '重复信息',
  'flag.spam': '垃圾信息或低质量',
  'flag.other': '其他',
  'flag.thankYou': '感谢——您的举报已记录，我们的管理员将进行审核。',
  'flag.reason': '原因',
  'flag.details': '详情',
  'flag.detailsPlaceholder': '这条信息有什么问题？',
  'flag.alreadyFlagged': '您已举报过此信息——感谢您帮助维护网站质量。',
  'flag.signInRequired': '您需要登录才能举报信息。',
  'flag.submitting': '提交中…',
  'flag.submitButton': '提交举报',

  'stats.views': '次浏览',
  'stats.applies': '次申请',

  'locale.showingJobs': '正在显示{country}的职位{langs}。',

  'category.browseByCategory': '按分类浏览',
  'category.noCategories': '暂无分类。',
  'category.uncategorised': '未分类',
  'category.notFound': '分类未找到',
  'category.notFoundMessage': '该分类（暂时）不存在。',
  'category.backToAll': '返回所有分类',
  'category.latestRoles': '{category}的最新职位——按分类筛选，按日期排序。',
  'category.noRolesOpen': '目前没有开放的{category}职位。',
  'category.browseAllJobs': '浏览全部职位 →',

  'status.applied': '已申请',
  'status.responded': '已回复',
  'status.interview': '面试已安排',
  'status.offer': '已收到录用',
  'status.rejected': '已拒绝',
  'status.hired': '已录用',
  'card.match': '%匹配',
  'card.new': '新',

  'cascade.yourPreferences': '你的偏好',
  'cascade.outside': '{country}以外',
  'cascade.worldwide': '全球',
  'cascade.loadingMore': '加载更多…',

  'feed.all': '全部',
  'feed.matches': '匹配',
  'feed.starred': '收藏',
  'feed.applied': '已申请',
  'feed.empty': '暂无内容。',
  'feed.tryAllFilter': '试试“全部”筛选。',
  'feed.loadError': '无法加载您的机会。',
  'feed.opportunities': '机会',

  'onboard.step': '第',
  'onboard.of': '步，共',
  'onboard.aboutYou': '关于您',
  'onboard.yourPreferences': '您的偏好',
  'onboard.choosePlan': '选择套餐',
  'onboard.aboutYouHint': '告诉我们您在寻找什么，以便我们为您推荐最相关的职位。',
  'onboard.preferencesHint': '我们将过滤掉不符合您所在地区和时区的职位。',
  'onboard.choosePlanHint': '您可以随时升级或取消。所有套餐均包含每周匹配推送至您的邮箱。',
  'onboard.targetJobTitle': '目标职位',
  'onboard.targetJobTitlePlaceholder': '例如：高级软件工程师',
  'onboard.experienceLevel': '经验水平',
  'onboard.entry': '入门（0–2年）',
  'onboard.junior': '初级（2–4年）',
  'onboard.mid': '中级（4–6年）',
  'onboard.senior': '高级（6–10年）',
  'onboard.lead': '主管（10年以上）',
  'onboard.executive': '高管',
  'onboard.jobSearchStatus': '求职状态',
  'onboard.activelyLooking': '积极求职中',
  'onboard.openToOffers': '对机会持开放态度',
  'onboard.casuallyBrowsing': '随便看看',
  'onboard.extraInfo': '补充信息',
  'onboard.extraInfoPlaceholder': '其他需要我们了解的信息——证书、签证状态、离职通知期等。',
  'onboard.salaryPlaceholder': '年薪金额',
  'onboard.targetSalary': '目标薪资',
  'onboard.uploadCV': '上传简历',
  'onboard.chooseFile': '选择文件',
  'onboard.cvFormats': 'PDF、DOCX、RTF 或 TXT · 最大 10 MB',
  'onboard.readyToUpload': '准备上传',
  'onboard.remove': '移除',
  'onboard.cvPrivacy': '您的简历仅用于匹配相关职位，未经您的操作不会与雇主共享。',
  'onboard.regions': '您可以工作的地区',
  'onboard.anywhere': '全球',
  'onboard.africa': '非洲',
  'onboard.europe': '欧洲',
  'onboard.northAmerica': '北美',
  'onboard.southAmerica': '南美',
  'onboard.asia': '亚洲',
  'onboard.oceania': '大洋洲',
  'onboard.timezones': '首选时区',
  'onboard.languages': '工作语言',
  'onboard.jobType': '工作类型',
  'onboard.fullTime': '全职',
  'onboard.partTime': '兼职',
  'onboard.contract': '合同制',
  'onboard.freelance': '自由职业',
  'onboard.internship': '实习',
  'onboard.country': '国家',
  'onboard.countryPlaceholder': '例如：中国',
  'onboard.planUpgradeHint': '您可以随时升级或取消。所有套餐均包含每周匹配推送至您的邮箱。',
  'onboard.matchesPerWeek': '每周最多 {count} 个匹配',
  'onboard.includesAgent': '包含专属顾问',
  'onboard.agreeTermsLabel': '我同意',
  'onboard.agreeTermsAnd': '和',
  'onboard.paymentRedirectHint': '您将被重定向到我们的支付合作伙伴完成订阅。您可以随时从面板取消。',
  'onboard.continueToPayment': '继续支付',
  'onboard.continue': '继续',
  'onboard.back': '返回',
  'onboard.submitting': '提交中…',
  'onboard.openingSignIn': '正在打开登录页面…',
  'onboard.draftSaveWarning': '无法保存您的进度。您的回答仍在这里，我们将在下一步重试。',
  'onboard.validationJobTitle': '请输入目标职位',
  'onboard.validationCVOrInfo': '请提供简历或介绍一下您自己',
  'onboard.validationCV': '请上传简历以继续',
  'onboard.validationCVSize': '简历大小不能超过 10 MB',
  'onboard.validationCVFormat': '请上传 PDF、DOCX、RTF 或 TXT 文件',
  'onboard.validationRegion': '请至少选择一个地区',
  'onboard.validationCountry': '请输入您的国家',
  'onboard.validationLanguage': '请至少选择一种语言',
  'onboard.validationJobType': '请至少选择一种工作类型',
  'onboard.validationTerms': '请在完成前同意服务条款',
  'onboard.cvDropHere': '将简历拖放到此处',
  'onboard.cvDragPrompt': '将简历拖放到此处，或点击浏览',
  'onboard.skipPreferences': '跳过 — 我稍后再设置',
  'onboard.skipPreferencesHint': '您可以随时在控制面板中进行配置',
  'onboard.features': '功能',
  'onboard.showLess': '显示更少',

  'dash.title': '我的面板',
  'dash.setupIncomplete': '设置未完成',
  'dash.finishPayment': '完成付款以启用匹配功能。',
  'dash.browseJobs': '浏览职位',
  'dash.changePlan': '更改套餐',
  'dash.viewPlans': '查看套餐',
  'dash.actionNeeded': '需要操作',
  'dash.choosePlan': '选择套餐',
  'dash.editPreferences': '编辑偏好',
  'dash.paymentReceived': '已收到付款——您的订阅已生效。',
  'dash.paymentFailed': '付款未完成。',
  'dash.paymentWaiting': '正在等待支付平台确认——通常不到一分钟。',
  'dash.matchPreferences': '匹配偏好',
  'dash.matchPreferencesHint': '选择您希望匹配的机会类型。我们只会对您已配置的类型运行匹配。',
  'dash.saving': '保存中…',
  'dash.preferencesSaved': '偏好已保存。',
  'dash.preferencesFailed': '无法保存偏好。',
  'dash.billing': '账单',
  'dash.active': '有效',
  'dash.renewsOn': '续订日期',
  'dash.changePlanOrCancel': '更改套餐或取消 →',
  'dash.signInTitle': '请登录以继续',
  'dash.signInHint': '面板展示您的套餐、匹配结果和收藏的职位。',
  'dash.yourAgent': '您的专属顾问',
  'dash.agentHint': '在您求职期间全程陪伴的私人招聘顾问。随时联系，当天回复。',
  'dash.emailAgent': '邮件',
  'dash.scheduleCall': '预约一对一通话',
  'dash.paymentPastDue': '上次付款未成功',
  'dash.updatePayment': '请更新付款信息以恢复匹配。',
  'dash.subCancelled': '您的订阅已取消',
  'dash.reactivateHint': '随时重新激活即可继续接收匹配结果。',
  'dash.finishSetup': '完成 {plan} 套餐的设置',
  'dash.matchingHint': '只有套餐生效后，我们才会对您的简历运行匹配引擎。整个过程只需两分钟。',
  'dash.openingPayment': '正在打开支付页面…',
  'dash.payPerMonth': '支付',
  'dash.perMonth': '/月',
  'dash.welcomeTitle': '欢迎来到 Stawi Opportunities！',
  'dash.welcomeBody': '我们为您匹配非洲各地的最佳机会。首先完善您的个人资料并设置偏好。',
  'dash.welcomeDismiss': '关闭',
  'dash.welcomeTour': '快速游览 →',
  'dash.statusActive': '活跃',
  'dash.statusPastDue': '逾期',
  'dash.statusCancelled': '已取消',
  'dash.emptyFeedTitle': '暂无机会',
  'dash.emptyFeedHint': '完成以下步骤开始接收匹配：',
  'dash.emptyFeedCompleteProfile': '完善个人资料',
  'dash.emptyFeedSetPreferences': '设置偏好',
  'dash.emptyFeedBrowseAll': '浏览所有机会',
  'dash.celebrationTitle': '一切就绪！',
  'dash.celebrationBody': '接下来：',
  'dash.celebrationStep1': '我们正在为您匹配机会',
  'dash.celebrationStep2': '您将收到新匹配的邮件通知',
  'dash.celebrationStep3': '您的首次匹配将在24小时内出现',
  'dash.celebrationDismiss': '明白了，显示我的控制面板',
  'onboard.profileSaved': '个人资料已保存！',

  'footer.jobSeekers': '求职者',
  'footer.browseJobs': '浏览职位',
  'footer.categories': '分类',
  'footer.advancedSearch': '高级搜索',
  'footer.company': '公司',
  'footer.about': '关于',
  'footer.faq': '常见问题',
  'footer.pricing': '定价',
  'footer.contact': '联系我们',
  'footer.legal': '法律信息',
  'footer.termsOfService': '服务条款',
  'footer.privacyPolicy': '隐私政策',
  'footer.privacy': '隐私',
  'footer.terms': '条款',
  'footer.rights': '保留所有权利。',
  'footer.madeBy': '出品方',
  'footer.explore': '浏览',
  'footer.jobs': '工作',
  'footer.scholarships': '奖学金',
  'footer.tenders': '招标',
  'footer.deals': '优惠',
  'footer.funding': '融资',
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
