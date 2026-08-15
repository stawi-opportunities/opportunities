import type { ReactElement } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { JobRow } from '../JobRow';
import type { SearchResult } from '@/types/search';
import { I18nProvider } from '@/i18n/I18nProvider';

function wrap(ui: ReactElement) {
  return render(<I18nProvider>{ui}</I18nProvider>);
}

const base: SearchResult = {
  id: '1',
  slug: 'senior-engineer-acme',
  title: 'Senior Engineer',
  apply_url: 'https://example.com/apply',
  company: 'Acme',
  location_text: 'Nairobi',
  country: 'KE',
  remote_type: 'remote',
  category: 'engineering',
  kind: 'job',
  salary_min: 0,
  salary_max: 0,
  currency: 'USD',
  posted_at: '2026-01-01T00:00:00Z',
  deadline: '2026-12-01T00:00:00Z',
  quality_score: 0.9,
  snippet: '',
  is_featured: false,
};

describe('JobRow', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-07-15T12:00:00Z'));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows deadline remaining time, not posted date', () => {
    wrap(<JobRow result={base} />);
    // short form for future deadline within 14 days of July 15 → Dec 1 is later
    // so absolute date
    expect(screen.queryByText(/posted/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/ago/i)).not.toBeInTheDocument();
    // Has a time element for the deadline
    expect(document.querySelector('time[data-date-source="deadline"]')).toBeTruthy();
  });

  it('marks past-due jobs as Expired', () => {
    wrap(
      <JobRow
        result={{
          ...base,
          deadline: '2026-07-01T00:00:00Z',
        }}
      />
    );
    expect(screen.getAllByText(/expired/i).length).toBeGreaterThan(0);
    expect(screen.getByRole('link', { name: /expired/i })).toBeInTheDocument();
  });

  it('does not show a date chip when there is no deadline', () => {
    wrap(
      <JobRow
        result={{
          ...base,
          deadline: null,
          posted_at: '2026-07-01T00:00:00Z',
        }}
      />
    );
    expect(document.querySelector('time')).toBeNull();
    expect(screen.queryByText(/posted/i)).not.toBeInTheDocument();
  });
});
