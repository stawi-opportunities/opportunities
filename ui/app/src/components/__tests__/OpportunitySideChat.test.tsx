import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { OpportunitySideChat } from '@/components/OpportunitySideChat';
import type { OpportunitySnapshot } from '@/types/snapshot';

const sendMeChat = vi.fn();
const fetchOnboardingDraft = vi.fn();

vi.mock('@/api/candidates', () => ({
  sendMeChat: (...a: unknown[]) => sendMeChat(...a),
  fetchOnboardingDraft: (...a: unknown[]) => fetchOnboardingDraft(...a),
}));

vi.mock('@/api/profile', () => ({
  uploadCV: vi.fn(),
  fetchCandidate: vi.fn(() => Promise.resolve(null)),
}));

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({ state: 'authenticated', login: vi.fn() }),
}));

const snap: OpportunitySnapshot = {
  schema_version: 1,
  kind: 'job',
  id: 'opp_1',
  slug: 'production-engineering',
  title: 'Production Engineering',
  issuing_entity: 'Meta',
  apply_url: 'https://example.com/apply',
  anchor_location: { city: 'Sunnyvale', country: 'US' },
};

function renderChat() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OpportunitySideChat snap={snap} />
    </QueryClientProvider>
  );
}

describe('OpportunitySideChat', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    fetchOnboardingDraft.mockResolvedValue({ step: 1, fields: {}, messages: [] });
    sendMeChat.mockResolvedValue({
      reply: 'Tell me more about your preferences.',
      fields: { target_job_title: 'Engineer' },
      missing: [],
      ready: false,
      messages: [
        { role: 'user', content: 'What jobs are available in Uganda' },
        { role: 'assistant', content: 'Tell me more about your preferences.' },
      ],
    });
  });

  it('renders side rail and asks a question', async () => {
    renderChat();
    await waitFor(() =>
      expect(screen.getByText(/You're viewing Production Engineering/i)).toBeInTheDocument()
    );
    expect(screen.getByLabelText(/Ask a question about this opportunity/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Upload resume/i })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/Ask a question about this opportunity/i), {
      target: { value: 'What jobs are available in Uganda' },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Send$/i }));

    await waitFor(() => expect(sendMeChat).toHaveBeenCalled());
    const firstCall = sendMeChat.mock.calls[0]?.[0] as { message: string };
    expect(firstCall.message).toMatch(/Production Engineering/);
    await waitFor(() =>
      expect(screen.getByText(/Tell me more about your preferences/i)).toBeInTheDocument()
    );
    // Current listing card surfaces after assistant reply.
    expect(screen.getAllByText('Production Engineering').length).toBeGreaterThan(0);
  });

  it('restores prior conversation for continuity', async () => {
    fetchOnboardingDraft.mockResolvedValue({
      step: 2,
      fields: { target_job_title: 'Engineer' },
      messages: [
        { role: 'user', content: 'Prior message' },
        { role: 'assistant', content: 'Prior reply from last session' },
      ],
    });
    renderChat();
    await waitFor(() =>
      expect(screen.getByText(/Prior reply from last session/i)).toBeInTheDocument()
    );
    expect(screen.getByText(/Prior message/i)).toBeInTheDocument();
  });
});
