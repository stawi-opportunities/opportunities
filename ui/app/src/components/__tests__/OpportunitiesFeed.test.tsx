import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { OpportunitiesFeed } from '../OpportunitiesFeed';
import * as api from '@/api/candidates';

vi.mock('@/api/candidates', async () => {
  return {
    fetchOpportunities: vi.fn(),
    starOpportunity: vi.fn(),
    unstarOpportunity: vi.fn(),
    applyToOpportunity: vi.fn(),
  };
});

const item = {
  opportunity_id: 'opp_1',
  score: 0.7,
  starred: false,
  created_at: '2026-05-23T10:00:00Z',
};

beforeEach(() => {
  vi.clearAllMocks();
  (api.fetchOpportunities as ReturnType<typeof vi.fn>).mockResolvedValue({ items: [item] });
  window.history.replaceState({}, '', '/dashboard/');
});

describe('OpportunitiesFeed', () => {
  it("fetches with the default 'all' filter on mount", async () => {
    render(<OpportunitiesFeed />);
    await waitFor(() => expect(api.fetchOpportunities).toHaveBeenCalled());
    expect(api.fetchOpportunities).toHaveBeenCalledWith({ filter: 'all' });
  });

  it('clicking the Starred chip refetches with filter=starred and updates the URL', async () => {
    render(<OpportunitiesFeed />);
    await waitFor(() => screen.getByRole('button', { name: /save opportunity/i }));
    fireEvent.click(screen.getByRole('tab', { name: /^starred$/i }));
    await waitFor(() =>
      expect(api.fetchOpportunities).toHaveBeenLastCalledWith({ filter: 'starred' })
    );
    expect(window.location.search).toBe('?filter=starred');
  });

  it('star click rolls back when the API rejects', async () => {
    (api.starOpportunity as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('network'));
    render(<OpportunitiesFeed />);
    await waitFor(() => screen.getByRole('button', { name: /save opportunity/i }));
    fireEvent.click(screen.getByRole('button', { name: /save opportunity/i }));
    // Optimistic flip:
    await waitFor(() => screen.getByRole('button', { name: /remove from saved/i }));
    // Rollback after the rejection settles:
    await waitFor(() => screen.getByRole('button', { name: /save opportunity/i }));
  });
});
