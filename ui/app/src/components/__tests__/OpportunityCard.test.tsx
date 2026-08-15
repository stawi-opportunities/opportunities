import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { OpportunityCard } from '../OpportunityCard';
import type { FeedItem } from '@/api/candidates';

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({
    state: 'authenticated',
    hasSession: true,
    ready: true,
    login: vi.fn(),
    logout: vi.fn(),
    runtime: {},
  }),
}));

vi.mock('@/hooks/useSubscription', () => ({
  useSubscription: () => ({
    data: { status: 'active', plan: 'pro' },
    isLoading: false,
    isError: false,
  }),
}));

const baseItem: FeedItem = {
  opportunity_id: 'opp_1',
  score: 0.82,
  starred: false,
  created_at: '2026-05-23T10:00:00Z',
};

const listingSnapshot = {
  title: 'Senior Engineer',
  company: 'Acme',
  location: 'Nairobi',
  kind: 'job',
  slug: 'senior-engineer-acme',
  id: 'opp_1',
};

describe('OpportunityCard', () => {
  it('renders the match score when present', () => {
    render(
      <OpportunityCard
        item={baseItem}
        snapshot={null}
        onStar={vi.fn()}
        onUnstar={vi.fn()}
        onApply={vi.fn()}
      />
    );
    expect(screen.getByTitle(/match score/i)).toHaveTextContent(/82/);
  });

  it('omits the match score when score is missing', () => {
    const item: FeedItem = { ...baseItem, score: undefined };
    render(
      <OpportunityCard
        item={item}
        snapshot={null}
        onStar={vi.fn()}
        onUnstar={vi.fn()}
        onApply={vi.fn()}
      />
    );
    expect(screen.queryByText(/%/)).not.toBeInTheDocument();
  });

  it('star button calls onStar when unstarred', () => {
    const onStar = vi.fn();
    render(
      <OpportunityCard
        item={baseItem}
        snapshot={null}
        onStar={onStar}
        onUnstar={vi.fn()}
        onApply={vi.fn()}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /save opportunity/i }));
    expect(onStar).toHaveBeenCalledWith('opp_1');
  });

  it('star button calls onUnstar when starred', () => {
    const onUnstar = vi.fn();
    const item = { ...baseItem, starred: true };
    render(
      <OpportunityCard
        item={item}
        snapshot={null}
        onStar={vi.fn()}
        onUnstar={onUnstar}
        onApply={vi.fn()}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /remove from saved/i }));
    expect(onUnstar).toHaveBeenCalledWith('opp_1');
  });

  it('shows Apply button when not applied; hides it once applied', () => {
    const onApply = vi.fn();
    const { rerender } = render(
      <OpportunityCard
        item={baseItem}
        snapshot={null}
        onStar={vi.fn()}
        onUnstar={vi.fn()}
        onApply={onApply}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: /apply/i }));
    expect(onApply).toHaveBeenCalledWith('opp_1');

    rerender(
      <OpportunityCard
        item={{
          ...baseItem,
          application: {
            status: 'applied',
            applied_at: baseItem.created_at,
            last_event_at: baseItem.created_at,
            method: 'manual',
          },
        }}
        snapshot={null}
        onStar={vi.fn()}
        onUnstar={vi.fn()}
        onApply={onApply}
      />
    );
    expect(screen.queryByRole('button', { name: /^apply$/i })).not.toBeInTheDocument();
    expect(screen.getByText(/applied/i)).toBeInTheDocument();
  });

  it('triage mode shows only Save and Dismiss, links body to listing', () => {
    const onStar = vi.fn();
    const onDismiss = vi.fn();
    const item: FeedItem = { ...baseItem, match_id: 'match_1' };
    render(
      <OpportunityCard
        item={item}
        snapshot={listingSnapshot}
        onStar={onStar}
        onUnstar={vi.fn()}
        onDismiss={onDismiss}
        actionsMode="triage"
      />
    );

    expect(screen.queryByRole('button', { name: /^apply$/i })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /view listing: senior engineer/i })).toHaveAttribute(
      'href',
      '/jobs/senior-engineer-acme/'
    );

    fireEvent.click(screen.getByRole('button', { name: /save opportunity/i }));
    expect(onStar).toHaveBeenCalledWith('opp_1');

    fireEvent.click(screen.getByRole('button', { name: /dismiss match/i }));
    expect(onDismiss).toHaveBeenCalledWith('match_1', 'opp_1');
  });

  it('triage mode without slug has no listing link', () => {
    const item: FeedItem = { ...baseItem, match_id: 'match_1' };
    render(
      <OpportunityCard
        item={item}
        snapshot={{ title: 'No slug role', kind: 'job' }}
        onStar={vi.fn()}
        onUnstar={vi.fn()}
        onDismiss={vi.fn()}
        actionsMode="triage"
      />
    );
    expect(screen.queryByRole('link', { name: /view listing/i })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /save opportunity/i })).toBeInTheDocument();
  });
});
