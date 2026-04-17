/**
 * /auth/callback/ — this page is only visible inside the OAuth popup for a
 * brief moment. The parent window polls popup.location, reads ?code=…,
 * closes the popup, and exchanges the code. This component renders a
 * spinner as courtesy while that handoff happens.
 */
export default function AuthCallback() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4 py-16">
      <div className="w-full max-w-md text-center">
        <div
          className="mx-auto h-8 w-8 animate-spin rounded-full border-4 border-accent-500 border-t-transparent"
          aria-label="Completing sign in"
        />
        <p className="mt-4 text-gray-600">Completing sign in…</p>
      </div>
    </div>
  );
}
