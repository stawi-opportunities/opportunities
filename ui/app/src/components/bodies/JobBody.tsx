import { useEffect, useRef } from "react";
import {
  jobEmploymentType,
  jobSeniority,
  type OpportunitySnapshot,
} from "@/types/snapshot";
import { fmtMoney } from "@/utils/format";
import { mountSanitisedHTML } from "@/utils/html";
import { useI18n } from "@/i18n/I18nProvider";
import FlagModal from "@/components/FlagModal";
import StatsLine from "@/components/StatsLine";

/**
 * JobBody renders the kind=job-specific portion of an OpportunityDetail
 * page: employment-type / seniority badges, salary, skills, and the
 * sanitised HTML description. The universal header (title, issuing
 * entity, deadline, anchor location, apply CTA) is already painted
 * upstream in OpportunityDetail.
 *
 * Inputs come from snap.attributes (employment_type, seniority, skills,
 * skills_nice_to_have, salary period). The description is preferred
 * from snap.description_html (legacy job snapshots, already passed
 * through bluemonday server-side) and falls back to plain-text
 * snap.description with whitespace preserved.
 */
export default function JobBody({ snap }: { snap: OpportunitySnapshot }) {
  const { t } = useI18n();
  const descRef = useRef<HTMLDivElement | null>(null);

  const employmentType = jobEmploymentType(snap);
  const seniority = jobSeniority(snap);

  const skillsRequired = stringArrayAttr(snap, "skills");
  const skillsNice = stringArrayAttr(snap, "skills_nice_to_have");

  const period = typeof snap.attributes?.salary_period === "string"
    ? (snap.attributes.salary_period as string)
    : "year";
  const money = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, period);

  useEffect(() => {
    const el = descRef.current;
    if (!el) return;
    el.replaceChildren();
    if (snap.description_html) {
      mountSanitisedHTML(el, snap.description_html);
    } else if (snap.description) {
      // Treat snap.description as plain text — DOM textContent assignment
      // never parses HTML, so we cannot inject script. Whitespace-pre-line
      // on the wrapper preserves paragraph breaks.
      el.textContent = snap.description;
    }
  }, [snap.description_html, snap.description]);

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {employmentType && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
            {employmentType}
          </span>
        )}
        {seniority && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs">
            {seniority}
          </span>
        )}
        {money && (
          <span className="font-medium text-emerald-700">{money}</span>
        )}
      </div>

      {(skillsRequired.length > 0 || skillsNice.length > 0) && (
        <section className="mt-8" aria-labelledby="skills-heading">
          <h2 id="skills-heading" className="sr-only">Skills</h2>
          {skillsRequired.length > 0 && (
            <>
              <h3 className="text-sm font-semibold text-gray-700">{t("job.skillsRequired")}</h3>
              <ul className="mt-2 flex flex-wrap gap-2">
                {skillsRequired.map((s) => (
                  <li key={s} className="rounded border border-navy-200 bg-navy-50 px-3 py-1 text-sm text-navy-900">
                    {s}
                  </li>
                ))}
              </ul>
            </>
          )}
          {skillsNice.length > 0 && (
            <>
              <h3 className="mt-4 text-sm font-semibold text-gray-700">{t("job.skillsNiceToHave")}</h3>
              <ul className="mt-2 flex flex-wrap gap-2">
                {skillsNice.map((s) => (
                  <li key={s} className="rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700">
                    {s}
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}

      <section
        ref={descRef}
        className="prose prose-slate mt-8 max-w-none whitespace-pre-line"
        aria-label="Job description"
      />

      <StatsLine slug={snap.slug} />
      <FlagModal slug={snap.slug} />
    </>
  );
}

function stringArrayAttr(snap: OpportunitySnapshot, key: string): string[] {
  const v = snap.attributes?.[key];
  if (!Array.isArray(v)) return [];
  return v.filter((x): x is string => typeof x === "string");
}
