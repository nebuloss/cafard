/**
 * The chat app's own class recipes, for the strings more than one of its
 * pieces draws with; the shared ones are in shared/ui.ts. As there, every
 * class is written out whole, so Tailwind finds it.
 *
 * The chat changes from one pane to two at 840px, not at a Tailwind
 * breakpoint: max-[840px] and min-[840px] say it where it matters.
 */
export const look = {
  /** A pane of the frame: the list or the conversation. */
  pane: 'relative flex min-h-0 min-w-0 flex-col bg-surface max-[840px]:group-data-[pane=list]/app:hidden',

  /** A round button holding one icon, filled: the main action of its bar. */
  iconBtnFilled: 'state-layer grid size-10 shrink-0 place-items-center rounded-full bg-primary text-on-primary',
  /** An icon button on the dark image viewer. */
  iconBtnDark: 'grid size-10 shrink-0 place-items-center rounded-full text-white hover:bg-white/12',

  /** A search box on a grey field; its size is the caller's. */
  search:
    'flex items-center gap-2 rounded-full bg-surface-high px-3 text-on-surface-variant [&_input]:min-w-0 [&_input]:flex-1 [&_input]:border-0 [&_input]:bg-transparent [&_input]:text-on-surface [&_input]:outline-none',

  /** A heading over a group of rows: a project in the list, a part of a sheet. */
  sec: 'px-4 pt-3.5 pb-1 text-[11.5px] font-bold tracking-[.06em] text-on-surface-variant uppercase',
  /** A line of text that says there is nothing, or that it is loading. */
  note: 'px-4 py-2 text-[12.5px] text-on-surface-variant',
  loading: 'px-4 py-10 text-on-surface-variant',
  /** What fills an empty pane. */
  empty: 'm-auto max-w-[340px] px-2 py-6 text-center text-on-surface-variant [&_h2]:mb-1.5 [&_h2]:text-[17px] [&_h2]:font-bold [&_h2]:text-balance [&_h2]:text-on-surface',
  pad: 'px-4 py-3',

  live: 'inline-flex h-5 shrink-0 items-center gap-[5px] rounded-full bg-tertiary-container px-2 text-[11.5px] font-semibold whitespace-nowrap text-on-tertiary-container before:size-1.5 before:rounded-full before:bg-success before:animate-[pulse_1.4s_infinite]',

  /** A sheet from the right: the whole screen on a phone, 380px beside it on a desktop. */
  sideSheet: 'flex h-full w-full flex-col bg-surface text-on-surface animate-sheet-in min-[840px]:w-[380px]',
  /** The log viewer: the whole screen on a phone, a large card on a desktop. */
  fullSheet:
    'flex min-w-0 flex-1 flex-col bg-surface text-on-surface min-[840px]:m-8 min-[840px]:max-w-[1100px] min-[840px]:overflow-hidden min-[840px]:rounded-[20px] min-[1164px]:mx-auto',
  sheetHead:
    'flex shrink-0 items-center gap-2.5 border-b border-outline-variant pt-[calc(10px+env(safe-area-inset-top,0px))] pr-3 pb-2.5 pl-4 min-[840px]:pt-2.5 [&_h3]:truncate [&_h3]:text-base [&_h3]:font-bold [&_p]:text-[12.5px] [&_p]:text-on-surface-variant [&_p]:tabular-nums',
  sheetScroll: 'min-h-0 flex-1 overflow-y-auto pb-[calc(16px+env(safe-area-inset-bottom,0px))]',

  /** A member or a sign-in in a sheet. */
  mem: 'flex items-center gap-3 px-4 py-2',
  memName: 'flex items-center gap-1.5 font-semibold',
  memSub: 'text-[12.5px] text-on-surface-variant',

  /** A file's name and size beside its icon, in a card or a row. */
  fileMain: 'min-w-0 flex-1',
  fileName: 'block truncate font-semibold',
  fileMeta: 'block text-xs text-on-surface-variant',
  fileCard: 'flex w-[min(360px,100%)] items-center gap-2.5 rounded-xl border border-outline-variant bg-surface-low p-2.5 text-left text-on-surface no-underline',
} as const;
