import { describe, expect, it } from 'vitest';
import { loadPage } from '@/testing/pages.ts';

describe('loadPage', () => {
  it('brings the body and its templates into the test document', () => {
    loadPage('live');
    expect(document.body.dataset['page']).toBe('live');
    const t = document.getElementById('tpl-line') as HTMLTemplateElement | null;
    expect(t?.tagName).toBe('TEMPLATE');
    expect(t?.content.querySelector('[data-slot="text"]')).not.toBeNull();
    expect(document.querySelectorAll('.log-list .line').length).toBeGreaterThan(0);
  });
});
