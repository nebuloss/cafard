import type { ImageProcessor } from '@/media/ImageProcessor.ts';

/** Sends the file as it is: GIFs, "keep original", and photos that do not decode. */
export class KeepOriginal implements ImageProcessor {
  process(file: Blob): Promise<Blob> {
    return Promise.resolve(file);
  }
}
