export type NullableScore = number | null;

export interface ScoreSet {
  performance: NullableScore;
  accessibility: NullableScore;
  bestPractices: NullableScore;
  seo: NullableScore;
  health: NullableScore;
}

export interface ScanSummary {
  id: string;
  inputUrl: string;
  rootUrl: string;
  status: string;
  phase: string;
  startedAt: string;
  finishedAt?: string;
  discoveredPages: number;
  completedPages: number;
  failedPages: number;
  fastCompletedPages: number;
  auditQueuedPages: number;
  auditCompletedPages: number;
  auditFailedPages: number;
  scores: ScoreSet;
  error?: string;
}

export interface OpenGraph {
  title: string;
  description: string;
  image: string;
  url: string;
  type: string;
  siteName: string;
}

export interface LinkInfo {
  href: string;
  url: string;
  text: string;
  target: string;
  rel: string;
  kind: 'internal' | 'external' | 'asset' | 'mail' | 'tel' | 'hash' | string;
  status?: number;
  pageUrl?: string;
  external: boolean;
}

export interface BlockInfo {
  name: string;
  variations: string[];
  sectionIndex: number;
}

export interface SectionInfo {
  index: number;
  variations: string[];
  blocks: string[];
}

export interface PageResult {
  url: string;
  statusCode: number;
  title: string;
  h1: string;
  canonical: string;
  description: string;
  robots: string;
  lang: string;
  og: OpenGraph;
  links: LinkInfo[];
  blocks: BlockInfo[];
  sections: SectionInfo[];
  blockCount: number;
  sectionCount: number;
  linkCount: number;
  internalLinks: number;
  externalLinks: number;
  lighthouse: ScoreSet;
  auditStatus: 'pending' | 'running' | 'complete' | 'failed' | string;
  auditError?: string;
  fetchError?: string;
}

export interface BlockStat {
  name: string;
  count: number;
  variations: Record<string, number>;
  pages: string[];
}

export interface SectionStat {
  variation: string;
  count: number;
  pages: string[];
}

export interface LinkStats {
  total: number;
  internal: number;
  external: number;
  asset: number;
  mail: number;
  tel: number;
  hash: number;
  uniqueInternal: number;
  uniqueExternal: number;
  uniqueAsset: number;
}

export interface SEOStats {
  missingTitle: number;
  missingDescription: number;
  missingH1: number;
  missingCanonical: number;
  missingOgTitle: number;
  missingOgImage: number;
  missingOgUrl: number;
}

export interface ScanResult {
  summary: ScanSummary;
  pages: PageResult[];
  blocks: BlockStat[];
  sections: SectionStat[];
  links: LinkStats;
  seo: SEOStats;
  generatedAt: string;
}

export interface ScanEvent {
  type: string;
  scanId: string;
  message?: string;
  pageUrl?: string;
  data?: unknown;
  timestamp: string;
}
