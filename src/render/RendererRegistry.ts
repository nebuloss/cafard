import type { Entry, EntryKind } from '@/model/types.ts';
import type { EntryRenderer } from '@/render/EntryRenderer.ts';

interface Rule {
  readonly matches: (entry: Entry) => boolean;
  readonly renderer: EntryRenderer;
}

/**
 * Picks the renderer of an entry: the first rule that matches (an entry kind,
 * or a predicate on meta), else the fallback. Track D registers its message
 * and question renderers here.
 */
export class RendererRegistry implements EntryRenderer {
  private readonly rules: Rule[] = [];

  constructor(private readonly fallback: EntryRenderer) {}

  register(kind: EntryKind, renderer: EntryRenderer): this {
    return this.when((e) => e.kind === kind, renderer);
  }

  /** A rule checked before the kind rules registered after it. */
  when(matches: (entry: Entry) => boolean, renderer: EntryRenderer): this {
    this.rules.push({ matches, renderer });
    return this;
  }

  render(entry: Entry): HTMLElement {
    for (const rule of this.rules) {
      if (rule.matches(entry)) return rule.renderer.render(entry);
    }
    return this.fallback.render(entry);
  }
}
