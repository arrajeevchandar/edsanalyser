import type { ComparedPage, ComparisonResult, ComparisonSummary, PageResult, ScanResult, ScanSummary } from './types';

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });
  if (!response.ok) {
    const payload = await response.json().catch(() => ({ error: response.statusText }));
    throw new Error(payload.error || response.statusText);
  }
  return response.json() as Promise<T>;
}

export function listScans(): Promise<ScanSummary[]> {
  return request<ScanSummary[] | null>('/api/scans').then((scans) => scans || []);
}

export function checkEds(url: string): Promise<{ isEDS: boolean; url: string }> {
  return request<{ isEDS: boolean; url: string }>('/api/eds-check', {
    method: 'POST',
    body: JSON.stringify({ url }),
  });
}

export function startScan(url: string, auditLimit: number | null): Promise<ScanSummary> {
  return request<ScanSummary>('/api/scans', {
    method: 'POST',
    body: JSON.stringify({ url, auditLimit, lighthouseMode: 'top', lighthouseLimit: 5 }),
  }).then(normalizeSummary);
}

export function getScan(id: string): Promise<ScanResult> {
  return request<ScanResult>(`/api/scans/${id}`).then(normalizeScanResult);
}

export function cancelScan(id: string): Promise<{ status: string }> {
  return request<{ status: string }>(`/api/scans/${id}/cancel`, { method: 'POST' });
}

export function reauditScan(id: string, mode: 'all' | 'top' = 'all'): Promise<ScanSummary> {
  return request<ScanSummary>(`/api/scans/${id}/audit`, {
    method: 'POST',
    body: JSON.stringify({ mode }),
  }).then(normalizeSummary);
}

export function listComparisons(): Promise<ComparisonSummary[]> {
  return request<ComparisonSummary[] | null>('/api/comparisons').then((comparisons) =>
    Array.isArray(comparisons) ? comparisons.map(normalizeComparisonSummary) : [],
  );
}

export function startComparison(sourceUrl: string, edsUrl: string, crawlLimit: number | null = null): Promise<ComparisonSummary> {
  return request<ComparisonSummary>('/api/comparisons', {
    method: 'POST',
    body: JSON.stringify({ sourceUrl, edsUrl, crawlLimit, crawlMode: 'exhaustive', renderedDiscovery: 'auto' }),
  }).then(normalizeComparisonSummary);
}

export function getComparison(id: string): Promise<ComparisonResult> {
  return request<ComparisonResult>(`/api/comparisons/${id}`).then(normalizeComparisonResult);
}

export function cancelComparison(id: string): Promise<{ status: string }> {
  return request<{ status: string }>(`/api/comparisons/${id}/cancel`, { method: 'POST' });
}

export function updateComparisonMatch(id: string, sourceUrl: string, edsUrl: string, action: 'match' | 'unmatch'): Promise<ComparisonResult> {
  return request<ComparisonResult>(`/api/comparisons/${id}/matches`, {
    method: 'POST',
    body: JSON.stringify({ sourceUrl, edsUrl, action }),
  }).then(normalizeComparisonResult);
}

export function runComparisonVisuals(id: string, pageKeys: string[] = []): Promise<ComparisonSummary> {
  return request<ComparisonSummary>(`/api/comparisons/${id}/visuals`, {
    method: 'POST',
    body: JSON.stringify({ pageKeys }),
  }).then(normalizeComparisonSummary);
}

function normalizeScanResult(result: ScanResult): ScanResult {
  const pages = (result.pages || []).map(normalizePage);
  return {
    ...result,
    summary: normalizeSummary(result.summary),
    pages,
    blocks: (result.blocks || []).map((block) => ({
      ...block,
      variations: block.variations || {},
      pages: block.pages || [],
    })),
    sections: (result.sections || []).map((section) => ({
      ...section,
      pages: section.pages || [],
    })),
    links: result.links || { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0, uniqueAsset: 0 },
    seo: result.seo || { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 },
  };
}

function normalizeComparisonResult(result: ComparisonResult): ComparisonResult {
  const matched = (result.matched || []).map(normalizeComparedPage);
  const uncertainMatches = (result.uncertainMatches || []).map(normalizeComparedPage);
  return {
    ...result,
    summary: normalizeComparisonSummary(result.summary),
    discovery: {
      source: normalizeDiscoveryReport(result.discovery?.source),
      eds: normalizeDiscoveryReport(result.discovery?.eds),
    },
    matched,
    uncertainMatches,
    missingInEDS: (result.missingInEDS || []).map(normalizePage),
    extraInEDS: (result.extraInEDS || []).map(normalizePage),
    sourceFetchFailures: (result.sourceFetchFailures || []).map(normalizePage),
    edsFetchFailures: (result.edsFetchFailures || []).map(normalizePage),
    blocks: (result.blocks || []).map((block) => ({
      ...block,
      variations: block.variations || {},
      pages: block.pages || [],
    })),
    sections: (result.sections || []).map((section) => ({
      ...section,
      pages: section.pages || [],
    })),
    links: result.links || {
      sourceTotal: 0,
      edsTotal: 0,
      missingInternal: 0,
      addedInternal: 0,
      missingExternal: 0,
      addedExternal: 0,
      missingAssets: 0,
      addedAssets: 0,
      matchedPageDiffs: 0,
    },
    seo: result.seo || { metadataDiffs: 0, titleDiffs: 0, h1Diffs: 0, descriptionDiffs: 0, ogDiffs: 0 },
  };
}

function normalizeSummary(summary: ScanSummary): ScanSummary {
  return {
    ...summary,
    phase: summary.phase || summary.status || 'idle',
    fastCompletedPages: summary.fastCompletedPages ?? summary.completedPages ?? 0,
    auditQueuedPages: summary.auditQueuedPages ?? 0,
    auditCompletedPages: summary.auditCompletedPages ?? 0,
    auditFailedPages: summary.auditFailedPages ?? 0,
    scores: summary.scores || { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
  };
}

function normalizeComparisonSummary(summary: ComparisonSummary): ComparisonSummary {
  return {
    ...summary,
    phase: summary.phase || summary.status || 'idle',
    fastReady: summary.fastReady ?? false,
    backgroundPhase: summary.backgroundPhase || '',
    sourcePages: summary.sourcePages ?? 0,
    edsPages: summary.edsPages ?? 0,
    sourceAnalyzed: summary.sourceAnalyzed ?? summary.sourcePages ?? 0,
    edsAnalyzed: summary.edsAnalyzed ?? summary.edsPages ?? 0,
    matchedPages: summary.matchedPages ?? 0,
    uncertainMatches: summary.uncertainMatches ?? 0,
    missingInEDS: summary.missingInEDS ?? 0,
    extraInEDS: summary.extraInEDS ?? 0,
    sourceFetchFailures: summary.sourceFetchFailures ?? 0,
    edsFetchFailures: summary.edsFetchFailures ?? 0,
    metadataDiffs: summary.metadataDiffs ?? 0,
    linkDiffs: summary.linkDiffs ?? 0,
    visualQueued: summary.visualQueued ?? 0,
    visualCompleted: summary.visualCompleted ?? 0,
    visualFailed: summary.visualFailed ?? 0,
    visualReview: summary.visualReview ?? 0,
    visualFail: summary.visualFail ?? 0,
    lighthouseQueued: summary.lighthouseQueued ?? 0,
    lighthouseCompleted: summary.lighthouseCompleted ?? 0,
    lighthouseFailed: summary.lighthouseFailed ?? 0,
    migrationScore: summary.migrationScore ?? null,
  };
}

function normalizeComparedPage(page: ComparedPage): ComparedPage {
  return {
    ...page,
    source: normalizePage(page.source || ({} as PageResult)),
    eds: normalizePage(page.eds || ({} as PageResult)),
    matchType: page.matchType || 'exact',
    matchConfidence: page.matchConfidence || 'high',
    matchReason: page.matchReason || page.matchType || 'exact',
    sourceAliases: page.sourceAliases || [],
    edsAliases: page.edsAliases || [],
    fieldDiffs: page.fieldDiffs || [],
    linkDiffs: page.linkDiffs || [],
    visuals: page.visuals || [],
    issues: page.issues || [],
    status: page.status || 'pass',
  };
}

function normalizeDiscoveryReport(report: ComparisonResult['discovery']['source'] | null | undefined): ComparisonResult['discovery']['source'] {
  return {
    rootUrl: report?.rootUrl || '',
    totalQueued: report?.totalQueued ?? 0,
    totalAnalyzed: report?.totalAnalyzed ?? 0,
    fromSitemap: report?.fromSitemap ?? 0,
    fromRobots: report?.fromRobots ?? 0,
    fromQueryIndex: report?.fromQueryIndex ?? 0,
    fromStaticLinks: report?.fromStaticLinks ?? 0,
    fromRenderedLinks: report?.fromRenderedLinks ?? 0,
    duplicates: report?.duplicates ?? 0,
    skippedAssets: report?.skippedAssets ?? 0,
    skippedExternal: report?.skippedExternal ?? 0,
    limitHit: report?.limitHit ?? false,
    warnings: report?.warnings || [],
  };
}

function normalizePage(page: PageResult): PageResult {
  const lighthouse = page.lighthouse || { performance: null, accessibility: null, bestPractices: null, seo: null, health: null };
  const auditStatus = page.auditStatus || (page.auditError ? 'failed' : lighthouse.health !== null ? 'complete' : 'pending');
  return {
    ...page,
    og: page.og || { title: '', description: '', image: '', url: '', type: '', siteName: '' },
    links: page.links || [],
    blocks: (page.blocks || []).map((block) => ({ ...block, variations: block.variations || [] })),
    sections: (page.sections || []).map((section) => ({
      ...section,
      variations: section.variations || [],
      blocks: section.blocks || [],
    })),
    lighthouse,
    auditStatus,
    matchCandidates: page.matchCandidates || [],
  };
}
