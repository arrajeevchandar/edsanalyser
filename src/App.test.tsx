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
  it('renders the dashboard shell and loads history', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse([]));
    render(<App />);
    expect(screen.getByText('EDS Analyser')).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.any(Object)));
  });

  it('handles empty API history encoded as null', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(null));
    render(<App />);
    expect(await screen.findByText('No scan selected')).toBeInTheDocument();
  });

  it('starts a scan from the URL form', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse([]))
      .mockResolvedValueOnce(jsonResponse({ isEDS: true, url: 'https://example.com/' }))
      .mockResolvedValueOnce(jsonResponse({
        id: 'scan-1',
        inputUrl: 'https://example.com',
        rootUrl: 'https://example.com/',
        status: 'running',
        startedAt: new Date().toISOString(),
        discoveredPages: 0,
        completedPages: 0,
        failedPages: 0,
        scores: { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
      }))
      .mockResolvedValueOnce(jsonResponse([]));

    render(<App />);
    await userEvent.type(screen.getByLabelText('EDS URL'), 'https://example.com');
    await userEvent.click(screen.getByTitle('Start scan'));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/scans', expect.objectContaining({ method: 'POST' })));
    expect(await screen.findByText('example.com')).toBeInTheDocument();
  });

  it('shows the "enter an EDS site" modal when the site is not EDS', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse([]))
      .mockResolvedValueOnce(jsonResponse({ isEDS: false, url: 'https://not-eds.com/' }));

    render(<App />);
    await userEvent.type(screen.getByLabelText('EDS URL'), 'https://not-eds.com');
    await userEvent.click(screen.getByTitle('Start scan'));

    expect(await screen.findByText('Enter an EDS site')).toBeInTheDocument();
    // The scan must not start when the site is not EDS.
    expect(fetchMock).not.toHaveBeenCalledWith('/api/scans', expect.objectContaining({ method: 'POST' }));
  });

  it('runs Lighthouse for all pages from the overview button', async () => {
    const summary = scanSummary('scan-audit');
    const result = {
      summary,
      pages: [{
        url: 'https://example.com/',
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
      }],
      blocks: [],
      sections: [],
      links: null,
      seo: null,
      generatedAt: new Date().toISOString(),
    };
    fetchMock
      .mockResolvedValueOnce(jsonResponse([summary]))   // listScans
      .mockResolvedValueOnce(jsonResponse(result))       // getScan
      .mockResolvedValueOnce(jsonResponse({ ...summary, status: 'running', phase: 'auditing' })); // reauditScan

    render(<App />);
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
    fetchMock
      .mockResolvedValueOnce(jsonResponse([summary]))
      .mockResolvedValueOnce(jsonResponse({
        summary,
        pages: [{
          url: 'https://example.com/',
          statusCode: 200,
          title: 'Home',
          h1: '',
          canonical: '',
          description: '',
          robots: '',
          lang: '',
          og: { title: 'Home', description: '', image: '', url: '', type: '', siteName: '' },
          links: null,
          blocks: null,
          sections: null,
          blockCount: 0,
          sectionCount: 0,
          linkCount: 0,
          internalLinks: 0,
          externalLinks: 0,
          lighthouse: { performance: null, accessibility: null, bestPractices: null, seo: null, health: null },
          auditStatus: '',
        }],
        blocks: null,
        sections: null,
        links: null,
        seo: null,
        generatedAt: new Date().toISOString(),
      }));

    render(<App />);
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

function scanSummary(id: string) {
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
  };
}

function jsonResponse(payload: unknown) {
  return {
    ok: true,
    json: async () => payload,
  } as Response;
}
