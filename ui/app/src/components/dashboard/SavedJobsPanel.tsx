import { Panel } from "./Panel";

export function SavedJobsPanel() {
  return (
    <Panel title="Saved jobs">
      <p className="text-sm text-gray-600">
        Save any listing with the bookmark icon and it'll appear here.
      </p>
      <a
        href="/jobs/"
        className="mt-4 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
      >
        Browse jobs →
      </a>
    </Panel>
  );
}
