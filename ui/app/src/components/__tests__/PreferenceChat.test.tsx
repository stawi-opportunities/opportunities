import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { PreferenceChat } from '@/components/preference-chat';

const sendMeChat = vi.fn();

vi.mock('@/api/candidates', () => ({
  sendMeChat: (...args: unknown[]) => sendMeChat(...args),
  saveOnboardingDraft: vi.fn(),
}));

vi.mock('@/api/profile', () => ({
  uploadCV: vi.fn(),
}));

describe('PreferenceChat', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sendMeChat.mockResolvedValue({
      reply: 'Got it — what country are you in?',
      fields: { target_job_title: 'Engineer' },
      missing: ['country'],
      ready: false,
      source: 'heuristic',
      messages: [
        { role: 'user', content: 'I am a software engineer' },
        { role: 'assistant', content: 'Got it — what country are you in?' },
      ],
    });
  });

  it('renders Meta-style landing with journey title, upload resume, and chips', () => {
    render(<PreferenceChat mode="intake" userName="Peter" showCompleteAction={false} />);
    expect(screen.getByText(/Hi Peter, let's start your opportunity journey/i)).toBeInTheDocument();
    expect(
      screen.getByText(/Share what you are looking for and be exact enough/i)
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/Describe what you want/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Upload resume/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Send$/i })).toBeInTheDocument();
    expect(screen.getByText(/Describe the role I want/i)).toBeInTheDocument();
    expect(screen.getByText(/Help me find suitable roles/i)).toBeInTheDocument();
  });

  it('restores thread from initialMessages instead of landing', () => {
    render(
      <PreferenceChat
        mode="intake"
        initialMessages={[
          { role: 'user', content: 'I am a product manager' },
          { role: 'assistant', content: 'What salary works for you?' },
        ]}
        initialFields={{ target_job_title: 'Product Manager' }}
        showCompleteAction
      />
    );
    expect(screen.getByText(/What salary works for you/i)).toBeInTheDocument();
    expect(screen.getByText(/Matching profile/i)).toBeInTheDocument();
    expect(screen.queryByText(/start your opportunity journey/i)).not.toBeInTheDocument();
  });

  it('with cvOnFile does not list capabilities as missing when other fields complete', async () => {
    sendMeChat.mockReset();
    sendMeChat.mockResolvedValue({
      reply: 'You are ready for a plan.',
      fields: {
        target_job_title: 'Engineer',
        experience_level: 'senior',
        job_types: ['Full-time'],
        salary_min: 90000,
        preferred_countries: ['KE'],
      },
      missing: ['capabilities'],
      ready: false,
      source: 'heuristic',
      messages: [
        { role: 'user', content: 'Ready' },
        { role: 'assistant', content: 'You are ready for a plan.' },
      ],
    });
    const onFieldsChange = vi.fn();
    render(
      <PreferenceChat
        mode="intake"
        cvOnFile
        showCompleteAction
        completeLabel="Continue to plans"
        onFieldsChange={onFieldsChange}
        initialMessages={[
          { role: 'user', content: 'hi' },
          { role: 'assistant', content: 'hello' },
        ]}
      />
    );
    const box = screen.getByRole('textbox', { name: /Describe what you want/i });
    fireEvent.change(box, { target: { value: 'All set' } });
    fireEvent.click(screen.getByRole('button', { name: /^Send$/i }));
    await waitFor(() => expect(onFieldsChange).toHaveBeenCalled());
    const last = onFieldsChange.mock.calls.at(-1);
    expect(last?.[1]?.ready).toBe(true);
    expect(last?.[1]?.missing ?? []).not.toContain('capabilities');
  });

  it('sends a turn, shows wait spinner, then Meta thread layout', async () => {
    let resolveChat!: (v: unknown) => void;
    sendMeChat.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveChat = resolve;
        })
    );
    const onFieldsChange = vi.fn();
    render(
      <PreferenceChat mode="intake" onFieldsChange={onFieldsChange} showCompleteAction={false} />
    );

    const box = screen.getByLabelText(/Describe what you want/i);
    fireEvent.change(box, { target: { value: 'I am a software engineer' } });
    fireEvent.click(screen.getByRole('button', { name: /^Send$/i }));

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Waiting for response/i })).toBeInTheDocument()
    );
    expect(box).toHaveValue('I am a software engineer');

    resolveChat({
      reply: 'Got it — what country are you in?',
      fields: { target_job_title: 'Engineer' },
      missing: ['country'],
      ready: false,
      source: 'heuristic',
      messages: [
        { role: 'user', content: 'I am a software engineer' },
        { role: 'assistant', content: 'Got it — what country are you in?' },
      ],
    });

    await waitFor(() =>
      expect(screen.getByText(/Got it — what country are you in/i)).toBeInTheDocument()
    );
    // Thread shows slim matching-profile control + bottom ask bar.
    expect(screen.getByText(/Matching profile/i)).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText(
        /Ask a question|role|resume|salary|countries|What role|experience/i
      )
    ).toBeInTheDocument();
    expect(onFieldsChange).toHaveBeenCalledWith(
      { target_job_title: 'Engineer' },
      expect.objectContaining({
        ready: false,
        messages: expect.arrayContaining([
          expect.objectContaining({ role: 'user' }),
          expect.objectContaining({ role: 'assistant' }),
        ]),
      })
    );
  });

  it('restores a prior conversation with onboarding card and right-aligned user turn', () => {
    render(
      <PreferenceChat
        mode="refine"
        initialFields={{ target_job_title: 'Nurse', country: 'KE' }}
        initialMessages={[
          { role: 'user', content: 'I am a nurse in Kenya' },
          { role: 'assistant', content: 'Great — anything to change?' },
        ]}
      />
    );
    expect(screen.getByText(/I am a nurse in Kenya/i)).toBeInTheDocument();
    expect(screen.getByText(/Great — anything to change/i)).toBeInTheDocument();
    expect(screen.getByText(/Matching profile/i)).toBeInTheDocument();
    expect(screen.queryByText(/Describe the role I want/i)).not.toBeInTheDocument();
    // Bottom thread composer (not the landing card chips).
    expect(screen.getByLabelText(/Describe what you want/i)).toBeInTheDocument();
  });

  it('shows CV as a filename chip in the thread', () => {
    render(
      <PreferenceChat
        mode="intake"
        initialMessages={[
          { role: 'user', content: 'Attached CV: Peter_Engineering_CV.pdf' },
          { role: 'assistant', content: 'Thanks! Tell me more about your preferences.' },
        ]}
      />
    );
    expect(screen.getByText('Peter_Engineering_CV.pdf')).toBeInTheDocument();
    expect(screen.queryByText(/Attached CV:/i)).not.toBeInTheDocument();
  });

  it('expands matching profile from the onboarding card', () => {
    render(
      <PreferenceChat
        mode="intake"
        showCompleteAction={false}
        initialMessages={[
          { role: 'user', content: 'hello' },
          { role: 'assistant', content: 'What role are you targeting?' },
        ]}
      />
    );
    fireEvent.click(screen.getByText(/Matching profile/i));
    expect(screen.getAllByText(/needed/i).length).toBeGreaterThan(0);
    expect(screen.getByText(/^Role$/i)).toBeInTheDocument();
  });
});
