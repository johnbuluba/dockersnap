// The cascade — three offset slabs encoding "snapshots accumulating
// through time" (rightward = forward, upward = newer). See the design
// rationale in docs/DASHBOARD-DESIGN.md (or this file's commit message).
//
// Inherits currentColor so a parent's `text-*` class controls the fill.
import type { JSX } from "preact";

export function Logo(props: JSX.SVGAttributes<SVGSVGElement>) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
      {...props}
    >
      <rect x="9" y="3" width="12" height="4" />
      <rect x="6" y="10" width="12" height="4" />
      <rect x="3" y="17" width="12" height="4" />
    </svg>
  );
}
