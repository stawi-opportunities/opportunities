import { describe, expect, it } from 'vitest';
import {
  heuristicExtract,
  isChatReady,
  localChatTurn,
  mergeChatFields,
  missingChatFields,
} from './chatHeuristic';

describe('chatHeuristic', () => {
  it('extracts a rich free-form preference message to ready=true', () => {
    const msg =
      'I am a senior software engineer in Kenya, actively looking for full-time remote roles in Africa. English speaker.';
    const res = localChatTurn(msg, {});
    expect(res.fields.experience_level).toBe('senior');
    expect(res.fields.country).toBe('KE');
    expect(res.fields.preferred_regions).toContain('Africa');
    expect(res.fields.preferred_languages).toContain('English');
    expect(res.fields.job_types?.length).toBeGreaterThan(0);
    expect(res.fields.target_job_title?.toLowerCase()).toMatch(/software engineer/);
    expect(res.ready).toBe(true);
    expect(res.missing).toEqual([]);
  });

  it('asks a follow-up when only a greeting is provided', () => {
    const res = localChatTurn('hello', {});
    expect(res.ready).toBe(false);
    expect(res.missing.length).toBeGreaterThan(0);
    expect(res.reply.toLowerCase()).toMatch(/role|looking|work/);
  });

  it('merges multi-turn answers until ready', () => {
    let draft = {};
    let r = localChatTurn('Looking for a product manager role', draft);
    draft = r.fields;
    expect(missingChatFields(draft)).toContain('country');

    r = localChatTurn('Based in Nigeria, mid-level, full-time, English, Africa region, actively looking', draft);
    expect(r.fields.country).toBe('NG');
    expect(r.fields.experience_level).toBe('mid');
    expect(isChatReady(r.fields)).toBe(true);
  });

  it('pulls a title from CV-like text', () => {
    const cv = `Jane Doe
Senior Backend Engineer
Nairobi, Kenya

Summary
Experienced Go developer with 7 years building APIs.`;
    const f = heuristicExtract(cv);
    expect(f.target_job_title?.toLowerCase()).toMatch(/backend engineer|senior backend/);
    expect(f.country).toBe('KE');
    expect(f.experience_level).toBe('senior');
  });

  it('merge prefers newer non-empty values', () => {
    const m = mergeChatFields(
      { target_job_title: 'A', country: 'KE' },
      { target_job_title: 'B', experience_level: 'senior' }
    );
    expect(m.target_job_title).toBe('B');
    expect(m.country).toBe('KE');
    expect(m.experience_level).toBe('senior');
  });
});
