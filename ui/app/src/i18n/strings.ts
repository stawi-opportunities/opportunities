// UI string catalog. English is the canonical and only shipped language.
// Keep this flat so missing-key fallbacks are a single lookup.

export type LangCode = 'en';

export const SUPPORTED_LANGS: LangCode[] = ['en'];

export const LANG_LABEL: Record<LangCode, string> = { en: 'English' };

export const RTL_LANGS = new Set<LangCode>();

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
  'nav.dashboard': string;
  'nav.overview': string;
  'nav.feed': string;
  'nav.matches': string;
  'nav.tools': string;
  'nav.saved': string;
  'nav.applications': string;
  'nav.preferences': string;
  'nav.billing': string;
  'nav.settings': string;

  // ---- Call-to-action buttons ----
  'cta.applyNow': string;
  'cta.signInToApply': string;
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
  'card.howToApply': string;
  'card.apply': string;

  // ---- Job detail ----
  'job.postedOn': string;
  'job.remote': string;
  'job.employmentType': string;
  'job.seniority': string;
  'job.salary': string;
  'job.skillsRequired': string;
  'job.skillsNiceToHave': string;
  'job.translatedNotice': string;
  'job.howToApply': string;
  'job.howToApplyLocked': string;
  'job.howToApplyEmpty': string;
  'howToApply.retry': string;
  'howToApply.fetchError': string;

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
  'error.feedLoad': string;

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
  'category.browseByIndustry': string;
  'category.refineBySector': string;
  'category.discover': string;
  'category.advancedSearch': string;
  'category.opportunityTypes': string;
  'category.chooseKind': string;
  'category.opportunity': string;
  'category.opportunities': string;

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
  'dash.breadcrumbDashboard': string;

  // ---- Profile completeness ----
  'dash.compTitle': string;
  'dash.compCountries': string;
  'dash.compLanguages': string;
  'dash.compLink': string;

  // ---- Quick actions ----
  'dash.qaBrowseDesc': string;
  'dash.qaSavedDesc': string;
  'dash.qaPrefsDesc': string;
  'dash.qaSettingsDesc': string;
  'dash.statTotal': string;
  'dash.statsError': string;
  'dash.profileCompleteness': string;
  'dash.gettingStarted': string;
  'dash.gsStep1a': string;
  'dash.gsProfile': string;
  'dash.gsStep1b': string;
  'dash.gsStep2a': string;
  'dash.gsPreferences': string;
  'dash.gsStep2b': string;
  'dash.gsBrowse': string;
  'dash.gsStep3b': string;

  // ---- Plan change modal ----
  'plan.changeTitle': string;
  'plan.currentPlan': string;
  'plan.newPlan': string;
  'plan.proratedAmount': string;
  'plan.nextBilling': string;
  'plan.confirmChange': string;
  'plan.changeSuccess': string;
  'plan.changeError': string;
  'plan.managedChangeHint': string;

  // ---- Cancel subscription modal ----
  'cancel.title': string;
  'cancel.reason': string;
  'cancel.foundJob': string;
  'cancel.tooExpensive': string;
  'cancel.notEnoughMatches': string;
  'cancel.justPausing': string;
  'cancel.other': string;
  'cancel.foundJobMessage': string;
  'cancel.tooExpensiveHint': string;
  'cancel.pauseHint': string;
  'cancel.confirmTitle': string;
  'cancel.confirmBody': string;
  'cancel.confirmCheckbox': string;
  'cancel.confirmButton': string;
  'cancel.success': string;

  // ---- Usage chart ----
  'usage.title': string;
  'usage.week': string;
  'usage.delivered': string;
  'usage.queued': string;
  'usage.noData': string;

  // ---- Invoice history ----
  'invoice.title': string;
  'invoice.date': string;
  'invoice.amount': string;
  'invoice.status': string;
  'invoice.download': string;
  'invoice.empty': string;

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

  // ---- Common ----
  'common.comingSoon': string;

  // ---- Settings ----
  'settings.title': string;
  'settings.sectionProfile': string;
  'settings.sectionNotifications': string;
  'settings.sectionSecurity': string;
  'settings.sectionAccount': string;
  'settings.sectionLanguage': string;
  'settings.sectionTheme': string;
  'settings.profile': string;
  'settings.name': string;
  'settings.email': string;
  'settings.phone': string;
  'settings.currentTitle': string;
  'settings.cv': string;
  'settings.cvUploaded': string;
  'settings.cvUploadNew': string;
  'settings.saveProfile': string;
  'settings.profileSaved': string;
  'settings.profileFailed': string;
  'settings.managedByIdp': string;
  'settings.notifications': string;
  'settings.emailDigest': string;
  'settings.emailDigestHint': string;
  'settings.daily': string;
  'settings.weekly': string;
  'settings.off': string;
  'settings.matchAlerts': string;
  'settings.matchAlertsHint': string;
  'settings.weeklySummary': string;
  'settings.weeklySummaryHint': string;
  'settings.marketingEmails': string;
  'settings.marketingEmailsHint': string;
  'settings.saveNotifications': string;
  'settings.notificationsSaved': string;
  'settings.notificationsFailed': string;
  'settings.comingSoon': string;
  'settings.security': string;
  'settings.changePassword': string;
  'settings.currentPassword': string;
  'settings.newPassword': string;
  'settings.confirmPassword': string;
  'settings.passwordChanged': string;
  'settings.passwordMismatch': string;
  'settings.passwordWeak': string;
  'settings.passwordSame': string;
  'settings.twoFactor': string;
  'settings.twoFactorDisabled': string;
  'settings.enable2FA': string;
  'settings.activeSessions': string;
  'settings.revokeSession': string;
  'settings.noSessions': string;
  'settings.account': string;
  'settings.deleteAccount': string;
  'settings.deleteAccountWarning': string;
  'settings.deleteConfirm': string;
  'settings.deleteReason': string;
  'settings.accountDeleted': string;
  'settings.dataExport': string;
  'settings.dataExportHint': string;
  'settings.dataExportRequested': string;
  'settings.language': string;
  'settings.uiLanguage': string;
  'settings.workingLanguages': string;
  'settings.country': string;
  'settings.theme': string;
  'settings.themeLight': string;
  'settings.themeDark': string;
  'settings.themeSystem': string;
  'settings.themeComingSoon': string;

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
  'settings.sessionManagementUnavailable': string;
}

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
  'nav.dashboard': 'Dashboard',
  'nav.overview': 'Overview',
  'nav.feed': 'Feed',
  'nav.matches': 'Matches',
  'nav.tools': 'Tools',
  'nav.saved': 'Saved',
  'nav.applications': 'Applications',
  'nav.preferences': 'Preferences',
  'nav.billing': 'Billing',
  'nav.settings': 'Settings',

  'cta.applyNow': 'Apply now',
  'cta.signInToApply': 'Sign in to apply',
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
  'card.howToApply': 'How to apply',
  'card.apply': 'Apply externally',
  'cta.twoMinutes': 'Two minutes to set up.',
  'cta.twoMinutesHint': 'Upload your CV, tell us what you want, we take it from there.',
  'cta.getStarted': 'Get started',

  'common.comingSoon': 'Coming soon',

  'job.postedOn': 'Posted',
  'job.remote': 'Remote',
  'job.employmentType': 'Employment type',
  'job.seniority': 'Seniority',
  'job.salary': 'Salary',
  'job.skillsRequired': 'Required skills',
  'job.skillsNiceToHave': 'Nice to have',
  'job.translatedNotice': 'Automatically translated from the original posting.',
  'job.howToApply': 'How to apply',
  'job.howToApplyLocked':
    'Application instructions are available to members. Subscribe to unlock email steps, contacts, and portal notes for this listing.',
  'job.howToApplyEmpty': 'No extra application steps on file — use the Apply button above.',
  'howToApply.retry': 'Retry',
  'howToApply.fetchError': 'Failed to load application instructions.',

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
  'error.feedLoad': 'Failed to load feed.',

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
  'category.browseByIndustry': 'Browse by industry',
  'category.refineBySector': 'Refine your search by sector or field.',
  'category.discover':
    'Discover opportunities across jobs, scholarships, tenders, deals and funding — all in one place.',
  'category.advancedSearch': 'Advanced search',
  'category.opportunityTypes': 'Opportunity types',
  'category.chooseKind': 'Choose the kind of opportunity you are looking for.',
  'category.opportunity': 'opportunity',
  'category.opportunities': 'opportunities',

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
  'dash.breadcrumbDashboard': 'Dashboard',
  'dash.compTitle': 'Add target job title',
  'dash.compCountries': 'Set preferred countries',
  'dash.compLanguages': 'Add languages',
  'dash.compLink': 'Complete profile →',
  'dash.qaBrowseDesc': 'Search all opportunities',
  'dash.qaSavedDesc': 'View bookmarked items',
  'dash.qaPrefsDesc': 'Update match settings',
  'dash.qaSettingsDesc': 'Profile & account',
  'dash.statTotal': 'Total',
  'dash.statsError': "Couldn't load dashboard stats.",
  'dash.profileCompleteness': 'Profile completeness',
  'dash.gettingStarted': 'Getting started',
  'dash.gsStep1a': 'Complete your',
  'dash.gsProfile': 'profile',
  'dash.gsStep1b': 'to improve your matches.',
  'dash.gsStep2a': 'Set your',
  'dash.gsPreferences': 'preferences',
  'dash.gsStep2b': 'so we match you with the right opportunities.',
  'dash.gsBrowse': 'Browse',
  'dash.gsStep3b': 'your feed and save or apply to opportunities.',

  'plan.changeTitle': 'Change your plan',
  'plan.currentPlan': 'Current plan',
  'plan.newPlan': 'New plan',
  'plan.proratedAmount': 'Prorated amount',
  'plan.nextBilling': 'Next billing date',
  'plan.confirmChange': 'Confirm change',
  'plan.changeSuccess': 'Plan changed to {plan}!',
  'plan.changeError': "Couldn't change plan. Try again.",
  'plan.managedChangeHint': 'Contact your agent to change plans.',

  'cancel.title': 'Cancel subscription',
  'cancel.reason': "What's the main reason?",
  'cancel.foundJob': 'I found a job already',
  'cancel.tooExpensive': 'Too expensive',
  'cancel.notEnoughMatches': 'Not enough matches',
  'cancel.justPausing': 'Just taking a break',
  'cancel.other': 'Other',
  'cancel.foundJobMessage': "Congratulations! We'll keep your profile in case things change.",
  'cancel.tooExpensiveHint': 'Did you know Pro is 5× the matches?',
  'cancel.pauseHint': 'You can pause instead. Matches resume when you return.',
  'cancel.confirmTitle': 'Are you sure?',
  'cancel.confirmBody':
    "Your subscription will end on {date}. You'll lose access to matching and your profile will be paused.",
  'cancel.confirmCheckbox': 'I understand my subscription will be cancelled',
  'cancel.confirmButton': 'Cancel subscription',
  'cancel.success': 'Your subscription has been cancelled.',

  'usage.title': 'Usage history',
  'usage.week': 'Week',
  'usage.delivered': 'Delivered',
  'usage.queued': 'Queued',
  'usage.noData': 'Usage data will appear after your first full week.',

  'invoice.title': 'Invoices',
  'invoice.date': 'Date',
  'invoice.amount': 'Amount',
  'invoice.status': 'Status',
  'invoice.download': 'Download',
  'invoice.empty': 'No invoices yet.',

  'settings.title': 'Settings',
  'settings.sectionProfile': 'Profile',
  'settings.sectionNotifications': 'Notifications',
  'settings.sectionSecurity': 'Security',
  'settings.sectionAccount': 'Account',
  'settings.sectionLanguage': 'Language',
  'settings.sectionTheme': 'Theme',
  'settings.profile': 'Profile',
  'settings.name': 'Full name',
  'settings.email': 'Email address',
  'settings.phone': 'Phone number',
  'settings.currentTitle': 'Current job title',
  'settings.cv': 'CV / Resume',
  'settings.cvUploaded': 'CV uploaded',
  'settings.cvUploadNew': 'Upload new CV',
  'settings.saveProfile': 'Save profile',
  'settings.profileSaved': 'Profile updated successfully.',
  'settings.profileFailed': 'Failed to update profile.',
  'settings.managedByIdp': 'Managed by your identity provider',
  'settings.notifications': 'Notifications',
  'settings.emailDigest': 'Email digest frequency',
  'settings.emailDigestHint':
    'How often we email your match or jobs summary. Daily users get every run; weekly users get the summary on the configured weekly day (default Monday).',
  'settings.daily': 'Daily',
  'settings.weekly': 'Weekly',
  'settings.off': 'Off',
  'settings.matchAlerts': 'Notify on every match',
  'settings.matchAlertsHint':
    'Optional. When off (default), we only send your scheduled digest summary — not a message for each new role.',
  'settings.weeklySummary': 'Job / match summaries',
  'settings.weeklySummaryHint':
    'Receive scheduled email summaries of matches (subscribers) or new jobs (free accounts). Turn off to stop all digests.',
  'settings.marketingEmails': 'Marketing emails',
  'settings.marketingEmailsHint': 'Tips, product updates, and job market insights.',
  'settings.saveNotifications': 'Save preferences',
  'settings.notificationsSaved': 'Notification preferences saved.',
  'settings.notificationsFailed': 'Failed to save notification preferences.',
  'settings.comingSoon': 'Coming soon',
  'settings.security': 'Security',
  'settings.changePassword': 'Change password',
  'settings.currentPassword': 'Current password',
  'settings.newPassword': 'New password',
  'settings.confirmPassword': 'Confirm new password',
  'settings.passwordChanged': 'Password changed successfully.',
  'settings.passwordMismatch': 'Passwords do not match.',
  'settings.passwordWeak': 'Password does not meet security requirements.',
  'settings.passwordSame': 'New password must be different from current password.',
  'settings.twoFactor': 'Two-factor authentication',
  'settings.twoFactorDisabled': 'Two-factor authentication is disabled.',
  'settings.enable2FA': 'Enable 2FA',
  'settings.activeSessions': 'Active sessions',
  'settings.revokeSession': 'Revoke',
  'settings.noSessions': 'No other active sessions.',
  'settings.account': 'Account',
  'settings.deleteAccount': 'Delete account',
  'settings.deleteAccountWarning':
    'This action is permanent and cannot be undone. All your data will be deleted.',
  'settings.deleteConfirm': 'Are you sure you want to delete your account?',
  'settings.deleteReason': "We're sorry to see you go. Tell us why (optional):",
  'settings.accountDeleted': 'Account deleted successfully.',
  'settings.dataExport': 'Export my data',
  'settings.dataExportHint': 'Download all your profile, matches, and activity data.',
  'settings.dataExportRequested':
    "Data export requested. You will receive an email when it's ready.",
  'settings.language': 'Language',
  'settings.uiLanguage': 'UI language',
  'settings.workingLanguages': 'Working languages',
  'settings.country': 'Country',
  'settings.theme': 'Theme',
  'settings.themeLight': 'Light',
  'settings.themeDark': 'Dark',
  'settings.themeSystem': 'System',
  'settings.themeComingSoon': 'Theme switching is coming soon.',

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
  'settings.sessionManagementUnavailable': 'Session management is not exposed in this release.',
};

export const CATALOG: Record<LangCode, Strings> = { en };

export type StringKey = keyof Strings;
