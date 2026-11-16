import type { ImageProcessor } from '@/media/ImageProcessor.ts';

/** A decoded image. */
export interface Bitmap {
  readonly width: number;
  readonly height: number;
  close(): void;
}

/** The browser parts the strategy uses, so a test can fake them. */
export interface CanvasEnv {
  /** Decodes honouring EXIF orientation, which also turns HEIC into pixels on iOS. */
  decode(file: Blob): Promise<Bitmap>;
  encode(bitmap: Bitmap, width: number, height: number, quality: number): Promise<Blob>;
}

export const browserCanvas: CanvasEnv = {
  decode: (file) => createImageBitmap(file, { imageOrientation: 'from-image' }),
  async encode(bitmap, width, height, quality) {
    const source = bitmap as unknown as ImageBitmap;
    if (typeof OffscreenCanvas !== 'undefined') {
      const canvas = new OffscreenCanvas(width, height);
      const ctx = canvas.getContext('2d');
      if (ctx === null) throw new Error('no 2d context');
      ctx.drawImage(source, 0, 0, width, height);
      return canvas.convertToBlob({ type: 'image/jpeg', quality });
    }
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const ctx = canvas.getContext('2d');
    if (ctx === null) throw new Error('no 2d context');
    ctx.drawImage(source, 0, 0, width, height);
    return new Promise((resolve, reject) => {
      canvas.toBlob((b) => (b === null ? reject(new Error('encoding failed')) : resolve(b)), 'image/jpeg', quality);
    });
  },
};

/**
 * Resizes a photo to a long edge of maxEdge and sends it as JPEG. Decoding
 * with the orientation applied and re-encoding also drops the EXIF block,
 * GPS position included. GIFs (maybe animated) and files that do not
 * decode go to the fallback, which sends them as they are.
 */
export class ResizeToJpeg implements ImageProcessor {
  constructor(
    private readonly fallback: ImageProcessor,
    private readonly env: CanvasEnv = browserCanvas,
    private readonly maxEdge = 2560,
    private readonly quality = 0.85,
  ) {}

  async process(file: Blob): Promise<Blob> {
    if (file.type === 'image/gif') return this.fallback.process(file);
    let bitmap: Bitmap;
    try {
      bitmap = await this.env.decode(file);
    } catch {
      return this.fallback.process(file);
    }
    try {
      const { width, height } = fit(bitmap.width, bitmap.height, this.maxEdge);
      return await this.env.encode(bitmap, width, height, this.quality);
    } catch {
      return this.fallback.process(file);
    } finally {
      bitmap.close();
    }
  }
}

/** The size within maxEdge, keeping the aspect ratio; never enlarged. */
export function fit(width: number, height: number, maxEdge: number): { width: number; height: number } {
  const scale = Math.min(1, maxEdge / Math.max(width, height, 1));
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}
