// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

/**
 * The visual kit: SVG charts and figures drawn with d3 scales and shapes,
 * coloured by design tokens. Import from here, not from the files.
 */

export { RiskIris, irisSegments, type RiskIrisProps, type IrisSize } from './RiskIris';
export { PipelineStrip, describePipeline, type PipelineStripProps } from './PipelineStrip';
export { PipelineSpark, type PipelineSparkProps } from './PipelineSpark';
export type { PipelinePoint } from './stripLayout';
export {
  Sparkline,
  Delta,
  type SparklineProps,
  type DeltaProps,
  type SparkTone,
} from './Sparkline';
export {
  SeverityBar,
  DistributionMini,
  type SeverityBarProps,
  type SeverityBarSize,
  type DistributionMiniProps,
} from './SeverityBar';
export { HeatStrip, rankRows, heatLevel, type HeatRow, type HeatStripProps } from './HeatStrip';
export { TrendArea, type TrendAreaProps, type TrendPoint } from './TrendArea';
export {
  AxisX,
  AxisY,
  ChartTooltip,
  EmptyChart,
  GateGlyph,
  GateLabel,
  GateShape,
  Gridlines,
  Legend,
  MarkLayer,
  SeverityGlyph,
  SeverityShape,
  SrTable,
  TipRow,
  gateLegend,
  severityLegend,
} from './frame';
export {
  RISK_SOURCE,
  RISK_WEIGHTS,
  describeTerm,
  explainRisk,
  riskDelta,
  riskPoints,
  riskTerms,
  type RiskDelta,
  type RiskTerm,
} from './risk';
export {
  GATE_NAME,
  SEVERITIES,
  SEVERITY_NAME,
  type GateState,
  type Severity,
  type SeverityCounts,
} from './severity';
export { useReducedMotion, useWidth } from './hooks';
