import type { Clock } from '@/core/Clock.ts';
import { Dom } from '@/core/Dom.ts';
import type { Answered } from '@/model/types.ts';
import { answerText } from '@/render/format.ts';
import { jobId, type UploadQueue } from '@/upload/UploadQueue.ts';
import { Component } from '@/components/Component.ts';

/**
 * Binds the choice buttons and the reply form of every question card
 * inside root, rendered by the server or by QuestionRenderer alike. A
 * click sends through the upload queue instead of posting the form; the
 * "answered" event then closes the card for everyone.
 */
export class QuestionCard extends Component {
  constructor(
    root: HTMLElement,
    private readonly queue: UploadQueue,
    private readonly clock: Clock,
  ) {
    super(root);
  }

  protected bind(): void {
    this.bag.listen(this.root, 'submit', (event) => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement) || !form.classList.contains('q-form')) return;
      event.preventDefault();
      const submitter = (event as SubmitEvent).submitter;
      const choice = submitter instanceof HTMLButtonElement && submitter.name === 'choice' ? submitter.value : undefined;
      const field = (name: string): string => Dom.find<HTMLInputElement>(`input[name="${name}"]`, form)?.value ?? '';
      const textInput = Dom.find<HTMLInputElement>('input[name="text"]', form);
      const text = textInput?.value ?? '';
      const replyTo = Number(field('reply_to'));
      if (!Number.isFinite(replyTo) || replyTo <= 0 || (choice === undefined && text.trim() === '')) return;
      if (textInput !== null) textInput.value = '';
      const card = form.closest<HTMLElement>('.question');
      if (card !== null && choice !== undefined) card.dataset['pending'] = choice;
      void this.queue.add({
        id: jobId(this.clock),
        chat: field('chat'),
        text,
        replyTo,
        ...(choice === undefined ? {} : { choice }),
        files: [],
        processed: true,
        keepOriginal: true,
        createdAt: this.clock.now(),
      });
    });
  }

  /** Closes the cards of an answered question. */
  answered(a: Answered): void {
    for (const card of Dom.findAll(`.question[data-seq="${a.seq}"]`, this.root)) closeCard(card, answerText(a.answer));
  }
}

/** A card once answered: "answered by X: choice", buttons disabled. */
export function closeCard(card: HTMLElement, answer: string): void {
  card.removeAttribute('data-open');
  delete card.dataset['pending'];
  for (const b of Dom.findAll<HTMLButtonElement>('button[name="choice"]', card)) b.disabled = true;
  Dom.setText(Dom.find('[data-slot="answer"]', card), answer);
}
