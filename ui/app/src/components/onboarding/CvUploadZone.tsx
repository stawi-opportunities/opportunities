import { useCallback, useId, useRef, useState } from 'react';
import type { StringKey } from '@/i18n/strings';

const ALLOWED_EXTS = /\.(pdf|docx?|rtf|txt)$/i;
const MAX_SIZE = 10 * 1024 * 1024;

interface Props {
  value: File | undefined;
  onChange: (file: File | undefined) => void;
  error?: string;
  t: (k: StringKey, fallback?: string) => string;
}

export function CvUploadZone({ value, onChange, error, t }: Props) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [dragging, setDragging] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const displayError = error ?? localError;

  const validateAndSet = useCallback(
    (f: File) => {
      setLocalError(null);
      if (!ALLOWED_EXTS.test(f.name)) {
        setLocalError(t('onboard.validationCVFormat'));
        return;
      }
      if (f.size > MAX_SIZE) {
        setLocalError(t('onboard.validationCVSize'));
        return;
      }
      onChange(f);
    },
    [onChange, t]
  );

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      setDragging(false);
      const f = e.dataTransfer.files?.[0];
      if (f) validateAndSet(f);
    },
    [validateAndSet]
  );

  return (
    <div>
      <div
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={handleDrop}
        onClick={() => inputRef.current?.click()}
        className={`cursor-pointer rounded-lg border-2 border-dashed p-6 text-center transition-colors ${
          value
            ? 'border-emerald-300 bg-emerald-50'
            : dragging
              ? 'border-accent-500 bg-accent-50'
              : displayError
                ? 'border-red-300 bg-red-50'
                : 'border-gray-300 bg-gray-50 hover:border-gray-400'
        }`}
      >
        {value ? (
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3 min-w-0">
              <svg className="h-8 w-8 shrink-0 text-emerald-600" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
              </svg>
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-gray-900">{value.name}</p>
                <p className="text-xs text-gray-500">
                  {(value.size / 1024).toFixed(1)} KB · {t('onboard.readyToUpload')}
                </p>
              </div>
            </div>
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                onChange(undefined);
              }}
              className="shrink-0 text-sm font-medium text-gray-600 hover:text-gray-900"
            >
              {t('onboard.remove')}
            </button>
          </div>
        ) : dragging ? (
          <div>
            <svg className="mx-auto h-8 w-8 text-accent-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 0115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
            </svg>
            <p className="mt-2 text-sm font-medium text-accent-700">{t('onboard.cvDropHere')}</p>
          </div>
        ) : (
          <div>
            <svg className="mx-auto h-8 w-8 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 0115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
            </svg>
            <p className="mt-2 text-sm text-gray-700">{t('onboard.cvDragPrompt')}</p>
            <p className="mt-1 text-xs text-gray-500">{t('onboard.cvFormats')}</p>
          </div>
        )}
      </div>

      <input
        ref={inputRef}
        id={id}
        type="file"
        accept=".pdf,.doc,.docx,.rtf,.txt"
        className="sr-only"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) validateAndSet(f);
        }}
      />

      {displayError && (
        <p className="mt-1 text-sm text-red-600" role="alert">
          {displayError}
        </p>
      )}
    </div>
  );
}
