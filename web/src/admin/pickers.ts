import { Component } from '@shared/component';
import { Html, html } from '@shared/html';
import { Icon } from '@shared/icons';
import { PickerState, PickerView, type PickItem } from '@shared/picker';
import { Avatar } from '@shared/widgets';
import type { AdminApp } from './app';
import { AdminStore } from './store';
import type { Member, Project } from './types';

/** The close button of a dialog. */
export function closeButton(): Html {
  return html`<button class="icon-btn" data-act="close" aria-label="Close">${Icon.of('close')}</button>`;
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
      : m.kind === 'agent' ? m.description || 'agent' : `Sends only${m.description ? ' · ' + m.description : ''}`,
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
    sub: p.archived ? 'Archived, read-only: nobody can be added' : `${p.conversations} conversations · ${p.members.length} members`,
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
    this.picker = new PickerView(state, { kinds, inline: false, placeholder: kinds ? 'Search by name or what it does' : 'Search projects', onChange: () => this.updateSave() });
    this.actions = {
      close: () => this.app.layer.close(),
      'pk-save': async () => {
        const { add, remove } = state.changes();
        this.app.layer.close();
        await this.app.change(() => this.save([...state.selected]), `Saved: ${add.length} added, ${remove.length} removed.`);
      },
    };
  }

  static forProject(app: AdminApp, p: Project): PickerDialog {
    const current = p.members.map((m) => m.name);
    return new PickerDialog(app, `Members of ${p.name}`, `Tick to add, untick to remove. People and agents see every conversation in ${p.name}; devices only send to it.`,
      new PickerState(memberItems(app.store.members), current, current), true,
      (members) => app.store.api.patch(`/projects/${AdminStore.path(p.name)}`, { members }));
  }

  static forMember(app: AdminApp, m: Member): PickerDialog {
    return new PickerDialog(app, `Projects of ${m.display_name || m.name}`, m.kind === 'device' ? 'It can send to the ticked projects, and read nothing.' : 'It sees every conversation in the ticked projects.',
      new PickerState(projectItems(app.store.projects, new Set(m.projects)), m.projects, m.projects), false,
      (projects) => app.store.api.patch(`/members/${AdminStore.path(m.name)}`, { projects }));
  }

  protected view(): Html {
    return html`<div class="m-head"><div class="t"><h2>${this.title}</h2><p>${this.sub}</p></div>${closeButton()}</div>
      <div class="m-body"><div class="pk-slot"></div></div>
      <div class="m-foot"><button class="btn tonal" data-act="close">Cancel</button><button class="btn filled" id="pkSave" data-act="pk-save" disabled>Save</button></div>`;
  }

  protected override afterRender(): void {
    this.$('.pk-slot').replaceWith(this.picker.root);
    this.picker.render();
    this.picker.focus();
  }

  private updateSave(): void {
    const btn = this.find<HTMLButtonElement>('#pkSave');
    if (!btn) return;
    const label = this.picker.state.saveLabel();
    btn.disabled = label === 'Save';
    btn.textContent = label;
  }
}
