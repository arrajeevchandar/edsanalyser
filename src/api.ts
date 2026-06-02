import type { PageResult, ScanResult, ScanSummary } from './types';

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
    links: result.links || { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0 },
    seo: result.seo || { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 },
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
  };
}
