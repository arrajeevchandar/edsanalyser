import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  globalThis.fetch = fetchMock;
  class FakeEventSource {
    url: string;
    onerror: (() => void) | null = null;
    constructor(url: string) {
      this.url = url;
    }
    addEventListener() {}
    removeEventListener() {}
    close() {}
  }
  // @ts-expect-error test shim
  globalThis.EventSource = FakeEventSource;
});

describe('App', () => {
  it('renders the compare-first dashboard shell and loads histories', async () => {
    mockHistories();

    render(<App />);

    expect(screen.getByText('EDS Analyser')).toBeInTheDocument();
    expect(await screen.findByText('No comparison selected')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.any(Object));
    expect(fetchMock).toHaveBeenCalledWith('/api/comparisons', expect.any(Object));
  });

  it('handles empty API histories encoded as null', async () => {
    mockHistories(null, null);

    render(<App />);

    expect(await screen.findByText('No comparison selected')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    expect(await screen.findByText('No scan selected')).toBeInTheDocument();
  });

  it('starts a scan from the URL form', async () => {
    const summary = scanSummary('scan-1', { status: 'running', phase: 'discovering' });
    mockHistories();
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ isEDS: true, url: 'https://example.com/' }))
      .mockResolvedValueOnce(jsonResponse(summary))
      .mockResolvedValueOnce(jsonResponse([summary]))
      .mockResolvedValueOnce(jsonResponse([]));

    render(<App />);
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await userEvent.type(screen.getByLabelText('EDS URL'), 'https://example.com');
    await userEvent.click(screen.getByTitle('Start scan'));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByText('example.com')).toBeInTheDocument();
  });

  it('shows the "enter an EDS site" modal when the site is not EDS', async () => {
    mockHistories();
    fetchMock.mockResolvedValueOnce(jsonResponse({ isEDS: false, url: 'https://not-eds.com/' }));

    render(<App />);
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await userEvent.type(screen.getByLabelText('EDS URL'), 'https://not-eds.com');
    await userEvent.click(screen.getByTitle('Start scan'));

    expect(await screen.findByText('Enter an EDS site')).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalledWith('/api/scans', expect.objectContaining({ method: 'POST' }));
  });

  it('starts a comparison from the two URL form', async () => {
    const summary = comparisonSummary('cmp-1', { status: 'running', phase: 'source-crawl' });
    mockHistories();
    fetchMock
      .mockResolvedValueOnce(jsonResponse(summary))
      .mockResolvedValueOnce(jsonResponse([]))
      .mockResolvedValueOnce(jsonResponse([summary]));

    render(<App />);
    await userEvent.type(screen.getByLabelText('Legacy site URL'), 'https://legacy.example.com');
    await userEvent.type(screen.getByLabelText('Migrated EDS URL'), 'https://eds.example.com');
    await userEvent.click(screen.getByTitle('Start comparison'));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/comparisons',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ sourceUrl: 'https://legacy.example.com', edsUrl: 'https://eds.example.com', crawlLimit: null, crawlMode: 'exhaustive', renderedDiscovery: 'auto' }),
        }),
      ),
    );
    expect(await screen.findByText(/legacy\.example\.com to eds\.example\.com/)).toBeInTheDocument();
  });

  it('renders comparison overview and page detail with missing visual diff data', async () => {
    const summary = comparisonSummary('cmp-detail');
    const result = comparisonResult(summary);
    mockHistories([], [summary]);
    fetchMock.mockResolvedValueOnce(jsonResponse(result));

    render(<App />);
    await userEvent.click(await screen.findByText(/legacy\.example\.com to eds\.example\.com/));

    expect(await screen.findByText('Matched paths')).toBeInTheDocument();
    expect(screen.getByText('Missing in EDS')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Pages' }));
    expect(await screen.findByText('Visual diff pending')).toBeInTheDocument();
    expect(screen.getByText('Legacy source')).toBeInTheDocument();
    expect(screen.getByText('Migrated EDS')).toBeInTheDocument();
    expect(screen.getByText('Missing in EDS')).toBeInTheDocument();
    expect(screen.getByText('Extra EDS')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Missing' }));
    expect(await screen.findByText('EDS page missing.')).toBeInTheDocument();
  });

  it('runs Lighthouse for all pages from the overview button', async () => {
    const summary = scanSummary('scan-audit');
    const result = scanResult(summary);
    mockHistories([summary], []);
    fetchMock
      .mockResolvedValueOnce(jsonResponse(result))
      .mockResolvedValueOnce(jsonResponse({ ...summary, status: 'running', phase: 'auditing' }));

    render(<App />);
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await userEvent.click(await screen.findByText('example.com'));
    await userEvent.click(await screen.findByRole('button', { name: /Run Lighthouse for all pages/i }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/scans/scan-audit/audit',
        expect.objectContaining({ method: 'POST', body: JSON.stringify({ mode: 'all' }) }),
      ),
    );
  });

  it('renders old scan data with null nested arrays without blanking tabs', async () => {
    const summary = scanSummary('scan-null');
    mockHistories([summary], []);
    fetchMock.mockResolvedValueOnce(jsonResponse({
      ...scanResult(summary),
      pages: [{
        ...pageResult('https://example.com/'),
        h1: '',
        links: null,
        blocks: null,
        sections: null,
        auditStatus: '',
      }],
      blocks: null,
      sections: null,
      links: null,
      seo: null,
    }));

    render(<App />);
    await userEvent.click(screen.getByRole('button', { name: 'Scan' }));
    await userEvent.click(await screen.findByText('example.com'));
    await userEvent.click(screen.getByRole('button', { name: 'Pages' }));
    expect(await screen.findByText('No links found')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Blocks' }));
    expect((await screen.findAllByText('No data yet')).length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole('button', { name: 'Links' }));
    expect(await screen.findByText('No links found yet')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'SEO / OG' }));
    expect(await screen.findByText('Home')).toBeInTheDocument();
  });
});

function mockHistories(scans: unknown = [], comparisons: unknown = []) {
  fetchMock
    .mockResolvedValueOnce(jsonResponse(scans))
    .mockResolvedValueOnce(jsonResponse(comparisons));
}

function scanSummary(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    inputUrl: 'https://example.com',
    rootUrl: 'https://example.com/',
    status: 'completed',
    phase: 'completed',
    startedAt: new Date().toISOString(),
    discoveredPages: 1,
    completedPages: 1,
    failedPages: 0,
    fastCompletedPages: 1,
    auditQueuedPages: 0,
    auditCompletedPages: 0,
    auditFailedPages: 0,
    scores: { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
    ...overrides,
  };
}

function scanResult(summary: ReturnType<typeof scanSummary>) {
  return {
    summary,
    pages: [pageResult('https://example.com/')],
    blocks: [],
    sections: [],
    links: null,
    seo: null,
    generatedAt: new Date().toISOString(),
  };
}

function comparisonSummary(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    sourceInputUrl: 'https://legacy.example.com',
    edsInputUrl: 'https://eds.example.com',
    sourceRootUrl: 'https://legacy.example.com/',
    edsRootUrl: 'https://eds.example.com/',
    status: 'completed',
    phase: 'completed',
    startedAt: new Date().toISOString(),
    sourcePages: 2,
    edsPages: 2,
    matchedPages: 1,
    uncertainMatches: 1,
    missingInEDS: 1,
    extraInEDS: 1,
    sourceFetchFailures: 1,
    edsFetchFailures: 0,
    metadataDiffs: 1,
    linkDiffs: 0,
    visualQueued: 2,
    visualCompleted: 0,
    visualFailed: 0,
    visualReview: 0,
    visualFail: 0,
    lighthouseQueued: 2,
    lighthouseCompleted: 0,
    lighthouseFailed: 0,
    migrationScore: 76,
    ...overrides,
  };
}

function comparisonResult(summary: ReturnType<typeof comparisonSummary>) {
  return {
    summary,
    discovery: {
      source: discoveryReport({ totalQueued: 5, totalAnalyzed: 4, fromSitemap: 2, fromStaticLinks: 2 }),
      eds: discoveryReport({ totalQueued: 5, totalAnalyzed: 4, fromQueryIndex: 2, fromRenderedLinks: 1, warnings: ['Rendered discovery was used because static discovery only found one page.'] }),
    },
    matched: [{
      path: '/',
      status: 'review',
      severity: 2,
      matchType: 'exact',
      matchConfidence: 'high',
      sourceAliases: ['exact:/'],
      edsAliases: ['exact:/'],
      source: pageResult('https://legacy.example.com/', { title: 'Legacy Home', h1: 'Legacy Home' }),
      eds: pageResult('https://eds.example.com/', { title: 'EDS Home', h1: 'EDS Home', blockCount: 2, sectionCount: 1 }),
      fieldDiffs: [{ field: 'title', source: 'Legacy Home', eds: 'EDS Home', status: 'review' }],
      linkDiffs: [],
      visuals: null,
      issues: ['Title changed'],
    }],
    uncertainMatches: [{
      path: '/alias',
      status: 'review',
      severity: 1,
      matchType: 'canonical',
      matchConfidence: 'medium',
      sourceAliases: ['exact:/alias'],
      edsAliases: ['canonical:/alias'],
      source: pageResult('https://legacy.example.com/alias', { title: 'Alias' }),
      eds: pageResult('https://eds.example.com/new-alias', { title: 'Alias', canonical: 'https://eds.example.com/alias' }),
      fieldDiffs: [],
      linkDiffs: [],
      visuals: [],
      issues: ['Matched by canonical alias; verify this page pair.'],
    }],
    missingInEDS: [pageResult('https://legacy.example.com/missing')],
    extraInEDS: [pageResult('https://eds.example.com/extra')],
    sourceFetchFailures: [pageResult('https://legacy.example.com/broken', { fetchError: 'HTTP 500', statusCode: 500 })],
    edsFetchFailures: [],
    blocks: [],
    sections: [],
    links: null,
    seo: null,
    generatedAt: new Date().toISOString(),
  };
}

function discoveryReport(overrides: Record<string, unknown> = {}) {
  return {
    rootUrl: 'https://example.com/',
    totalQueued: 0,
    totalAnalyzed: 0,
    fromSitemap: 0,
    fromRobots: 0,
    fromQueryIndex: 0,
    fromStaticLinks: 0,
    fromRenderedLinks: 0,
    duplicates: 0,
    skippedAssets: 0,
    skippedExternal: 0,
    limitHit: false,
    warnings: [],
    ...overrides,
  };
}

function pageResult(url: string, overrides: Record<string, unknown> = {}) {
  return {
    url,
    statusCode: 200,
    title: 'Home',
    h1: 'Home',
    canonical: '',
    description: '',
    robots: '',
    lang: '',
    og: { title: 'Home', description: '', image: '', url: '', type: '', siteName: '' },
    links: [],
    blocks: [],
    sections: [],
    blockCount: 0,
    sectionCount: 0,
    linkCount: 0,
    internalLinks: 0,
    externalLinks: 0,
    lighthouse: { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
    auditStatus: 'pending',
    ...overrides,
  };
}

function jsonResponse(payload: unknown) {
  return {
    ok: true,
    json: async () => payload,
  } as Response;
}
