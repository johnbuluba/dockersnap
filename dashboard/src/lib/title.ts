import { useEffect } from "preact/hooks";

// Sync document.title so browser-tab labels are useful when the user has
// multiple dashboards / instances open. Format mirrors the CLI prompt:
// "dockersnap" alone for the root, "dockersnap › instances" for sections,
// "dockersnap › instances › foo" for an instance detail.
export function useDocumentTitle(parts: Array<string | undefined | null>) {
  useEffect(() => {
    const segments = ["dockersnap", ...parts.filter(Boolean) as string[]];
    document.title = segments.join(" › ");
  }, [parts.join("§")]);
}
