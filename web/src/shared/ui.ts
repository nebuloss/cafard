/**
 * The class recipes every component draws with: claudication's primitives
 * (web/src/ui/primitives there: button, surface, table, field, chip),
 * written as Tailwind class strings so a TypeScript class can use them in
 * its markup without a framework. The colours are the shared design's
 * tokens (src/design/theme.css): bg-primary, text-on-surface-variant, …
 *
 * Tailwind finds a class by reading these files, so every class is written
 * out whole here, never assembled from pieces.
 */

export type ButtonVariant = 'filled' | 'tonal' | 'outlined' | 'text' | 'danger' | 'danger-text';

const BASE_BTN =
  'state-layer inline-flex items-center justify-center rounded-md3-full font-medium whitespace-nowrap no-underline disabled:pointer-events-none disabled:opacity-40 [&_.ic]:size-[18px]';

const BTN: Record<ButtonVariant, string> = {
  filled: 'bg-primary text-on-primary',
  tonal: 'bg-secondary-container text-on-secondary-container',
  outlined: 'border border-outline text-primary',
  text: 'text-primary',
  danger: 'bg-error text-on-error',
  'danger-text': 'border border-outline-variant text-error',
};

const SIZE = { md: 'h-10 gap-2 px-5 text-sm', sm: 'h-8 shrink-0 gap-1.5 px-3 text-[13px]' } as const;

export const ui = {
  /** A button, or a link drawn as one. */
  btn(variant: ButtonVariant = 'tonal', size: 'md' | 'sm' = 'md'): string {
    return `${BASE_BTN} ${SIZE[size]} ${BTN[variant]}`;
  },
  /** A round button holding one icon. */
  iconBtn: 'state-layer grid size-10 shrink-0 place-items-center rounded-full text-on-surface-variant',

  /** A card: a tonal surface, its title and actions on one line (claudication's Card). */
  card: 'flex min-w-0 flex-col gap-4 rounded-md3-xl bg-surface-container p-4 sm:p-6',
  cardHead: 'flex min-h-9 flex-wrap items-center justify-between gap-x-4 gap-y-3',
  cardTitle: 'mr-auto text-xl leading-7 font-normal text-on-surface',
  cardAside: 'flex flex-wrap items-center gap-2 lg:flex-nowrap',
  /** Cards side by side, as many as the width takes. */
  cards: 'grid items-start gap-4 [grid-template-columns:repeat(auto-fit,minmax(min(26rem,100%),1fr))]',

  /** A table on its card (claudication's Table). */
  tableWrap: 'isolate overflow-x-auto rounded-md3-l bg-surface-lowest ring-1 ring-outline-variant/60',
  table: 'w-full min-w-[34rem] border-collapse text-sm',
  th: 'px-3 py-2.5 text-left text-[11px] font-semibold tracking-wider text-on-surface-variant uppercase first:pl-5 last:pr-5',
  td: 'px-3 py-2.5 align-middle first:pl-5 last:pr-5',
  tr: 'border-b border-outline-variant/50 last:border-0',
  /** A row that opens its page. */
  trLink: 'cursor-pointer border-b border-outline-variant/50 transition-colors last:border-0 hover:bg-surface-low',

  /** A field's label and its control. */
  field: 'flex flex-col gap-2',
  label: 'text-xs font-semibold text-on-surface-variant',
  input:
    'h-11 w-full rounded-md3-m border border-outline bg-transparent px-4 text-sm text-on-surface outline-none transition-colors hover:border-on-surface focus:border-2 focus:border-primary focus:px-[15px] aria-invalid:border-error',
  textarea:
    'w-full min-h-24 resize-y rounded-md3-m border border-outline bg-transparent px-4 py-2.5 text-sm leading-snug text-on-surface outline-none transition-colors hover:border-on-surface focus:border-2 focus:border-primary focus:px-[15px] focus:py-[9px]',
  error: 'text-[13px] text-error',
  /** A search box. */
  search:
    'flex h-10 min-w-36 flex-[0_1_15rem] items-center gap-2 rounded-md3-full bg-surface-lowest px-4 text-on-surface-variant [&_input]:min-w-0 [&_input]:flex-1 [&_input]:border-0 [&_input]:bg-transparent [&_input]:text-sm [&_input]:text-on-surface [&_input]:outline-none',

  /** Segmented buttons; the pressed one is aria-pressed. */
  seg: 'inline-flex shrink-0 overflow-hidden rounded-md3-full border border-outline',
  segBtn:
    'state-layer h-10 border-r border-outline px-3.5 text-[13px] font-medium text-on-surface-variant last:border-0 aria-pressed:bg-secondary-container aria-pressed:text-on-secondary-container',

  /** Small labels. */
  chip: 'inline-flex h-6 items-center gap-1 rounded-md3-s bg-surface-high px-2 text-xs font-semibold text-on-surface no-underline',
  pill: {
    ok: 'inline-flex h-5 items-center rounded-full bg-success-container px-2 text-[11.5px] font-semibold whitespace-nowrap text-on-success-container',
    warn: 'inline-flex h-5 items-center rounded-full bg-warning-container px-2 text-[11.5px] font-semibold whitespace-nowrap text-on-warning-container',
    err: 'inline-flex h-5 items-center rounded-full bg-error-container px-2 text-[11.5px] font-semibold whitespace-nowrap text-on-error-container',
    muted: 'inline-flex h-5 items-center rounded-full bg-surface-high px-2 text-[11.5px] font-semibold whitespace-nowrap text-on-surface-variant',
  },
  kind: {
    agent: 'rounded-md3-xs bg-tertiary-container px-1.5 text-[10.5px] font-bold tracking-wide text-on-tertiary-container uppercase',
    device: 'rounded-md3-xs bg-warning-container px-1.5 text-[10.5px] font-bold tracking-wide text-on-warning-container uppercase',
    person: '',
  } as Record<string, string>,

  /**
   * Term and value pairs, claudication's KeyValue: the term a small capital
   * label, the value beside it, on one baseline.
   */
  kv: 'm-0 grid grid-cols-[max-content_1fr] items-baseline gap-x-6 gap-y-2 text-sm [&_dd]:m-0 [&_dd]:min-w-0 [&_dd]:break-words [&_dt]:text-[11px] [&_dt]:font-semibold [&_dt]:tracking-wider [&_dt]:text-on-surface-variant [&_dt]:uppercase',
  /** A small capital label over a value. */
  overline: 'text-[11px] font-semibold tracking-wider text-on-surface-variant uppercase',

  /** A screen's body: one column of full-width cards, a step apart (claudication's Page). */
  page: 'flex flex-col gap-5',
  /** A figure on a tile (claudication's Stat): its label, the value, a hint. */
  stat: 'flex min-w-0 flex-col rounded-md3-xl bg-surface-container px-5 py-4 text-inherit no-underline',
  statValue: 'mt-1 truncate text-2xl leading-8 font-normal tabular-nums',
  statHint: 'mt-0.5 truncate text-xs text-on-surface-variant',
  /** Stats side by side, two to a row on a phone; one layout per count. */
  statGrid: {
    3: 'grid gap-4 sm:grid-cols-3',
    4: 'grid grid-cols-2 gap-4 sm:grid-cols-4',
    5: 'grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5',
  } as Record<number, string>,
  /** A line: a thing on the left, its action on the right. */
  line: 'flex items-center gap-3 [&_.m]:min-w-0 [&_.m]:flex-1 [&_.m_b]:block [&_.m_b]:truncate [&_.m_b]:font-semibold [&_.m_span]:text-[12.5px] [&_.m_span]:text-on-surface-variant',
  /** A link in running text. */
  link: 'font-semibold text-primary no-underline hover:underline',
  dim: 'text-on-surface-variant',
  empty: 'p-5 text-center text-on-surface-variant',
  /** A warning across the page. */
  notice: 'flex items-center gap-3 rounded-md3-m bg-warning-container px-4 py-2.5 text-on-warning-container [&_.ic]:text-warning',
  /** A box that confirms something that cannot be undone. */
  confirm: 'flex flex-col gap-2.5 rounded-md3-m bg-error-container p-3.5 text-on-error-container [&_ul]:m-0 [&_ul]:pl-5',
  /** What a dialog is made of. */
  dialogHead: 'flex items-center gap-3 px-6 pt-4 pb-3 [&_h2]:text-xl [&_h2]:font-normal [&_p]:mt-0.5 [&_p]:text-[13px] [&_p]:text-on-surface-variant',
  dialogBody: 'flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-6 pt-2 pb-4',
  dialogFoot: 'flex flex-wrap justify-end gap-2 border-t border-outline-variant/60 px-6 pt-4 pb-5',
} as const;
