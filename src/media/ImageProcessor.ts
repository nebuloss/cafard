/** Strategy (PLAN 9.4): what happens to a photo before it is sent. */
export interface ImageProcessor {
  process(file: Blob): Promise<Blob>;
}
