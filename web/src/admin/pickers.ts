import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { n, t } from '@shared/i18n';
import { Icon } from '@shared/icons';
import { PickerState, PickerView, type PickItem } from '@shared/picker';
import { ui } from '@shared/ui';
import { Avatar } from '@shared/widgets';
import type { AdminApp } from './app';
import { AdminStore } from './store';
import type { Member, Project } from './types';

/** The close button of a dialog. */
export function closeButton(): Html {
  return html`<button class="${ui.iconBtn}" data-act="close" aria-label="${t('Close')}">${Icon.of('close')}</button>`;
}

/** Items for the member picker, from the store. */
export function memberItems(members: Member[]): PickItem[] {
  return members.map((m) => ({
    id: m.name,
    label: m.display_name || m.name,
    kind: m.kind,
    avatar: Avatar.of(m, 'sm'),
    text: [m.name, m.display_name, m.description].join(' ').toLowerCase(),
    sub: m.kind === 'person' ? `@${m.name}${m.projects.length ? ' · ' + m.projects.join(', ') : ''}`
      : m.kind === 'agent' ? m.description || t('agent') : `${t('Sends only')}${m.description ? ' · ' + m.description : ''}`,
  }));
}

/** Items for the project picker, from the store. */
export function projectItems(projects: Project[], current: Set<string>): PickItem[] {
  return projects.map((p) => ({
    id: p.name,
    label: p.name,
    avatar: Avatar.project(p.name, 'sm'),
    text: p.name,
    disabled: !!p.archived && !current.has(p.name),
    sub: p.archived ? t('Archived, read-only: nobody can be added') : `${n(p.conversations, '{n} conversation', '{n} conversations')} · ${n(p.members.length, '{n} member', '{n} members')}`,
  }));
}

/**
 * PickerDialog is "Add or remove members" on a project and "Add or remove
 * projects" on a member: one picker, saved as the full set, which the server
 * applies one change at a time through access.
 */
export class PickerDialog extends Component {
  private readonly picker: PickerView;

  private constructor(
    private readonly app: AdminApp,
    private readonly title: string,
    private readonly sub: string,
    state: PickerState,
    kinds: boolean,
    private readonly save: (selected: string[]) => Promise<unknown>,
  ) {
    super('div');
    this.picker = new PickerView(state, { kinds, inline: false, placeholder: kinds ? t('Search by name or what it does') : t('Search projects'), onChange: () => this.updateSave() });
    this.actions = {
      close: () => this.app.layer.close(),
      'pk-save': async () => {
        const { add, remove } = state.changes();
        this.app.layer.close();
        await this.app.change(() => this.save([...state.selected]), t('Saved: {added} added, {removed} removed.', { added: add.length, removed: remove.length }));
      },
    };
  }

  static forProject(app: AdminApp, p: Project): PickerDialog {
    const current = p.members.map((m) => m.name);
    return new PickerDialog(app, t('Members of {name}', { name: p.name }), t('People and agents see every conversation in {name}; devices only send.', { name: p.name }),
      new PickerState(memberItems(app.store.members), current, current), true,
      (members) => app.store.api.patch(`/projects/${AdminStore.path(p.name)}`, { members }));
  }

  static forMember(app: AdminApp, m: Member): PickerDialog {
    return new PickerDialog(app, t('Projects of {name}', { name: m.display_name || m.name }), m.kind === 'device' ? t('It can send to the ticked projects, and read nothing.') : t('It sees every conversation in the ticked projects.'),
      new PickerState(projectItems(app.store.projects, new Set(m.projects)), m.projects, m.projects), false,
      (projects) => app.store.api.patch(`/members/${AdminStore.path(m.name)}`, { projects }));
  }

  protected view(): Html {
    return html`<div class="${ui.dialogHead}"><div class="min-w-0 flex-1"><h2>${this.title}</h2><p>${this.sub}</p></div>${closeButton()}</div>
      <div class="${ui.dialogBody}"><div class="pk-slot"></div></div>
      <div class="${ui.dialogFoot}"><button class="${ui.btn('tonal')}" data-act="close">${t('Cancel')}</button><button class="${ui.btn('filled')}" id="pkSave" data-act="pk-save" disabled>${t('Save')}</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.picker.focus();
  }

  private updateSave(): void {
    const btn = this.find<HTMLButtonElement>('#pkSave');
    if (!btn) return;
    // Whether anything changed is asked of the state, not read off the
    // label, which is in the page's language.
    const { add, remove } = this.picker.state.changes();
    btn.disabled = !add.length && !remove.length;
    btn.textContent = this.picker.state.saveLabel();
  }
}
