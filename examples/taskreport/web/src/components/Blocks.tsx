// The JSON renderer.
//
// One component per block type from the view schema, and nothing here knows
// what a task is. A prompt about invoices would render through exactly these
// components, because the model's answer is a document rather than an app
// screen.
//
// Charts are hand-drawn SVG on purpose. A charting library is a lot of
// dependency for four shapes, and drawing them here keeps the whole render
// path readable — which matters when the input is written by a model.

import type { Block, ViewDocument } from "../api";

export function DocumentView({ document }: { document: ViewDocument }) {
  // A refusal is an answer, and it has to look unlike one. Rendering it as a
  // lone text block among stat cards is how "I cannot see that" gets read as a
  // finding.
  if (document.unavailable) {
    return (
      <article className="doc refusal">
        <p className="refusal-text">{document.unavailable}</p>
      </article>
    );
  }

  return (
    <article className="doc">
      {document.title && <h2 className="doc-title">{document.title}</h2>}
      {(document.blocks ?? []).map((block, index) => (
        <BlockView key={index} block={block} />
      ))}
    </article>
  );
}

function BlockView({ block }: { block: Block }) {
  switch (block.type) {
    case "stats":
      return (
        <section className="block">
          <Heading text={block.heading} />
          <div className="stats">
            {block.items.map((item, index) => (
              <div key={index} className={`stat tone-${item.tone ?? "none"}`}>
                <span className="stat-value">{format(item.value)}</span>
                <span className="stat-label">{item.label}</span>
              </div>
            ))}
          </div>
        </section>
      );

    case "bar":
      return (
        <section className="block">
          <Heading text={block.heading} />
          <Bars series={block.series} />
        </section>
      );

    case "line":
      return (
        <section className="block">
          <Heading text={block.heading} />
          <Line series={block.series} />
        </section>
      );

    case "table":
      return (
        <section className="block">
          <Heading text={block.heading} />
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  {block.columns.map((column) => (
                    <th key={column}>{column}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {block.rows.map((row, index) => (
                  <tr key={index}>
                    {row.map((cell, cellIndex) => (
                      <td key={cellIndex}>{cell}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      );

    case "text":
      return (
        <section className="block">
          <Heading text={block.heading} />
          <p className="prose">{block.text}</p>
        </section>
      );
  }
}

function Heading({ text }: { text?: string }) {
  return text ? <h3 className="block-heading">{text}</h3> : null;
}

// Horizontal bars, so a long category name has somewhere to go. A vertical
// chart would need rotated labels at exactly the width where they stop fitting.
function Bars({ series }: { series: { label: string; value: number }[] }) {
  const max = Math.max(...series.map((point) => point.value), 1);
  return (
    <div className="bars">
      {series.map((point, index) => (
        <div key={index} className="bar-row">
          <span className="bar-label" title={point.label}>
            {point.label}
          </span>
          <div className="bar-track">
            <div className="bar-fill" style={{ width: `${(point.value / max) * 100}%` }} />
          </div>
          <span className="bar-value">{format(point.value)}</span>
        </div>
      ))}
    </div>
  );
}

// A sparkline with an area fill and the endpoint marked, drawn in a viewBox so
// it scales with the panel instead of needing a resize observer.
function Line({ series }: { series: { label: string; value: number }[] }) {
  const width = 600;
  const height = 160;
  const pad = 8;
  const max = Math.max(...series.map((point) => point.value), 1);
  const step = series.length > 1 ? (width - pad * 2) / (series.length - 1) : 0;

  const at = (index: number, value: number) => {
    const x = pad + index * step;
    const y = height - pad - (value / max) * (height - pad * 2);
    return [x, y] as const;
  };

  const points = series.map((point, index) => at(index, point.value));
  const path = points.map(([x, y], index) => `${index === 0 ? "M" : "L"}${x},${y}`).join(" ");
  const area = `${path} L${points[points.length - 1][0]},${height - pad} L${points[0][0]},${height - pad} Z`;
  const [lastX, lastY] = points[points.length - 1];

  return (
    <div className="line">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="trend over time">
        <path d={area} className="line-area" />
        <path d={path} className="line-stroke" />
        <circle cx={lastX} cy={lastY} r="4" className="line-dot" />
      </svg>
      <div className="line-labels">
        <span>{series[0]?.label}</span>
        <span>{series[series.length - 1]?.label}</span>
      </div>
    </div>
  );
}

// Whole numbers stay whole; a rate keeps one decimal. Rendering 4.000000001
// because it came back as a float is the kind of detail that makes a report
// look untrustworthy.
function format(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(1);
}
