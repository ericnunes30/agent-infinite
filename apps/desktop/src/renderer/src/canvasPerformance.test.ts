import { describe, expect, it } from 'vitest';
import {
  BULK_START_CONCURRENCY,
  canvasLayoutSignature,
  TERMINAL_PREVIEW_BATCH_MS,
} from './canvasPerformance';

describe('canvas terminal performance', () => {
  it('does not invalidate layout persistence when only terminal preview changes', () => {
    const base = {
      id: 'agent-1',
      position: { x: 120, y: 80 },
      width: 300,
      height: 210,
      data: { preview: 'first output' },
    };
    const changedPreview = { ...base, data: { preview: 'new output' } };
    expect(canvasLayoutSignature([base], [], { x: 0, y: 0, zoom: 1 })).toBe(
      canvasLayoutSignature([changedPreview], [], { x: 0, y: 0, zoom: 1 }),
    );
  });

  it('caps combined preview commits at ten frames per second', () => {
    expect(TERMINAL_PREVIEW_BATCH_MS).toBeGreaterThanOrEqual(100);
  });

  it('limits simultaneous CLI startups to avoid a CPU spike', () => {
    expect(BULK_START_CONCURRENCY).toBe(2);
  });
});
