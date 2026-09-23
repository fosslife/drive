// One drawn mark set, one stroke system: 16px box, 1.5 stroke, square caps and
// mitre joins. The squared-off geometry is the drafted, institutional register
// the rest of the interface is set in — a rounded stroke would read as a
// consumer app wearing a document's clothes.
//
// These replaced the emoji the first iteration used. An emoji is whatever the
// operating system decides it is: it changes shape per platform, ignores the
// palette, and cannot take a state.

const box = {
  viewBox: '0 0 16 16',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.5,
  strokeLinecap: 'square',
  strokeLinejoin: 'miter',
  'aria-hidden': true,
  focusable: false,
}

// A container with its tab, drawn the way a folder is drawn on a shelf label.
export const Folder = (props) => (
  <svg {...box} {...props}>
    <path d="M1.75 4.25h4.5l1.5 1.75h6.5v7.25h-12.5z" />
  </svg>
)

// A sheet with its corner turned: the fold is the second stroke, not a shading.
export const Sheet = (props) => (
  <svg {...box} {...props}>
    <path d="M3.25 1.75h6l3.75 3.75v8.75h-9.75z" />
    <path d="M9.25 1.75v4h3.75" />
  </svg>
)

// A sheet carrying a horizon and a disc: the mark for photographic material.
export const Plate = (props) => (
  <svg {...box} {...props}>
    <path d="M2.25 3.25h11.5v9.5h-11.5z" />
    <path d="M2.25 10.5l3.25-3 2.5 2.25 2.5-2.75 3.25 3" />
    <path d="M6 6.25h.01" strokeWidth="2" />
  </svg>
)

export const Glass = (props) => (
  <svg {...box} {...props}>
    <circle cx="6.75" cy="6.75" r="4.5" />
    <path d="M10.25 10.25l3.5 3.5" />
  </svg>
)

// Surveying ticks: the mark that stands next to the count while the reconciler
// is still walking the disk.
export const Survey = (props) => (
  <svg {...box} {...props}>
    <path d="M1.5 12.5h13" />
    <path d="M3.5 12.5v-3M7 12.5v-6M10.5 12.5v-4M14 12.5v-8" />
  </svg>
)

export const Left = (props) => (
  <svg {...box} {...props}>
    <path d="M10 2.5l-6 5.5 6 5.5" />
  </svg>
)

export const Right = (props) => (
  <svg {...box} {...props}>
    <path d="M6 2.5l6 5.5-6 5.5" />
  </svg>
)

export const Cross = (props) => (
  <svg {...box} {...props}>
    <path d="M3 3l10 10M13 3l-10 10" />
  </svg>
)
