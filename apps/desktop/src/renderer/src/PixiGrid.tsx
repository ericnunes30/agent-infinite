import { Application, Graphics } from 'pixi.js';
import { useEffect, useRef, useState } from 'react';

interface PixiGridProps {
  readonly viewport: { readonly x: number; readonly y: number; readonly zoom: number };
  readonly theme: 'dark' | 'light';
}

export function PixiGrid({ viewport, theme }: PixiGridProps): React.JSX.Element {
  const hostRef = useRef<HTMLDivElement>(null);
  const graphicsRef = useRef<Graphics | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return undefined;
    const app = new Application();
    let disposed = false;
    void app.init({ resizeTo: host, backgroundAlpha: 0, antialias: true }).then(() => {
      if (disposed) return;
      host.appendChild(app.canvas);
      const graphics = new Graphics();
      graphicsRef.current = graphics;
      app.stage.addChild(graphics);
      setReady(true);
    });
    return () => {
      disposed = true;
      graphicsRef.current = null;
      app.destroy(true);
    };
  }, []);

  useEffect(() => {
    const graphics = graphicsRef.current;
    const host = hostRef.current;
    if (!graphics || !host) return;
    graphics.clear();
    const spacing = 34 * viewport.zoom;
    const offsetX = ((viewport.x % spacing) + spacing) % spacing;
    const offsetY = ((viewport.y % spacing) + spacing) % spacing;
    for (let x = offsetX; x < host.clientWidth; x += spacing) {
      graphics.moveTo(x, 0).lineTo(x, host.clientHeight);
    }
    for (let y = offsetY; y < host.clientHeight; y += spacing) {
      graphics.moveTo(0, y).lineTo(host.clientWidth, y);
    }
    graphics.stroke({
      color: theme === 'light' ? 0x3e563f : 0xb7f34a,
      alpha: theme === 'light' ? 0.08 : 0.055,
      width: 1,
    });
  }, [ready, theme, viewport]);

  return <div className="pixi-grid" ref={hostRef} aria-hidden="true" />;
}
