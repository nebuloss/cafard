import { DisposableBag, type Disposable } from '@/core/Disposable.ts';
import { Dom, h } from '@/core/Dom.ts';
import type { PrincipalKind, Scope, TokenCreated } from '@/model/types.ts';
import { ApiError } from '@/net/ApiError.ts';
import type { Services } from '@/App.ts';
import type { Page } from '@/pages/Page.ts';
import { mountShell, type Shell } from '@/pages/Shell.ts';

/**
 * /agents: creates tokens through the API instead of a page load, and shows
 * the new one once in the token dialog, with its snippets. Revoking stays a
 * plain form, with a confirmation.
 */
export class AgentsPage implements Page {
  constructor(
    private readonly doc: Document,
    private readonly services: Services,
  ) {}

  start(): Disposable {
    const bag = new DisposableBag();
    const shell = mountShell(this.doc, this.services, bag, '/api/events?scope=status');
    const form = Dom.find<HTMLFormElement>('[data-component="TokenForm"]', this.doc);
    const dialog = Dom.find('[data-component="TokenDialog"]', this.doc);
    if (form !== null && dialog !== null) {
      bag.listen(form, 'submit', (event) => {
        event.preventDefault();
        void this.create(form, dialog, shell);
      });
    }
    if (dialog !== null) {
      for (const button of Dom.findAll<HTMLButtonElement>('[data-copy="token"]', dialog)) {
        bag.listen(button, 'click', () => void this.copy(Dom.find('[data-slot="token"]', dialog)?.textContent ?? '', shell));
      }
    }
    bag.add(shell.feed.start());
    return bag;
  }

  private async create(form: HTMLFormElement, dialog: HTMLElement, shell: Shell): Promise<void> {
    const data = new FormData(form);
    const submit = Dom.find<HTMLButtonElement>('button[type="submit"]', form);
    if (submit !== null) submit.disabled = true;
    try {
      const created = await this.services.api.createToken({
        principal: String(data.get('principal') ?? '').trim(),
        kind: String(data.get('kind') ?? 'agent') as PrincipalKind,
        label: String(data.get('label') ?? '').trim(),
        scopes: data.getAll('scopes').map((s) => String(s) as Scope),
      });
      showToken(dialog, created);
      form.reset();
    } catch (error) {
      shell.toast?.show(error instanceof ApiError ? error.message : 'Creating the token failed.', 'error');
    } finally {
      if (submit !== null) submit.disabled = false;
    }
  }

  private async copy(text: string, shell: Shell): Promise<void> {
    try {
      await this.doc.defaultView?.navigator.clipboard.writeText(text);
      shell.toast?.show('Copied.');
    } catch {
      shell.toast?.show('This browser refused to copy; select the token and copy it by hand.', 'error');
    }
  }
}

/** Fills the server-rendered token dialog with a new token and shows it. */
export function showToken(dialog: HTMLElement, created: TokenCreated): void {
  Dom.setText(Dom.find('[data-slot="principal"]', dialog), created.principal);
  Dom.setText(Dom.find('[data-slot="token"]', dialog), created.token);
  const snippets = Dom.find('[data-slot="snippets"]', dialog);
  snippets?.replaceChildren(
    ...created.snippets.map((s) =>
      h('figure', { class: 'snippet' }, h('figcaption', { class: 'label' }, s.label), h('pre', { class: 'mono', 'data-language': s.language }, h('code', {}, s.text))),
    ),
  );
  dialog.hidden = false;
  dialog.focus();
}
