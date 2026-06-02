import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  BarChart3,
  Boxes,
  ExternalLink,
  FileSearch,
  Globe2,
  History,
  Link2,
  Loader2,
  OctagonX,
  Play,
  Search,
  ShieldCheck,
  StopCircle,
  TriangleAlert,
  X,
} from 'lucide-react';
import { cancelScan, checkEds, getScan, listScans, reauditScan, startScan } from './api';
import type { BlockStat, PageResult, ScanEvent, ScanResult, ScanSummary, SectionStat } from './types';

type Tab = 'overview' | 'pages' | 'blocks' | 'links' | 'seo' | 'history';

const tabs: Array<{ id: Tab; label: string; icon: typeof Activity }> = [
  { id: 'overview', label: 'Overview', icon: Activity },
  { id: 'pages', label: 'Pages', icon: FileSearch },
  { id: 'blocks', label: 'Blocks', icon: Boxes },
  { id: 'links', label: 'Links', icon: Link2 },
  { id: 'seo', label: 'SEO / OG', icon: ShieldCheck },
  { id: 'history', label: 'History', icon: History },
];

export default function App() {
  const [url, setUrl] = useState('');
  const [scan, setScan] = useState<ScanResult | null>(null);
  const [history, setHistory] = useState<ScanSummary[]>([]);
  const [activeScan, setActiveScan] = useState<ScanSummary | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [pageFilter, setPageFilter] = useState('');
  const [selectedPageURL, setSelectedPageURL] = useState<string | null>(null);
  const [events, setEvents] = useState<ScanEvent[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [notEdsOpen, setNotEdsOpen] = useState(false);

  useEffect(() => {
    void refreshHistory();
  }, []);

  useEffect(() => {
    if (!activeScan || activeScan.status !== 'running') {
      return undefined;
    }
    const source = new EventSource(`/api/scans/${activeScan.id}/events`);
    const eventNames = [
      'start',
      'discovered',
      'warning',
      'page-start',
      'page-analyzed',
      'fast-complete',
      'audit-start',
      'audit-complete',
      'audit-error',
      'cancel',
      'complete',
    ];
    const handleEvent = (message: MessageEvent) => {
      const parsed = JSON.parse(message.data) as ScanEvent;
      setEvents((current) => [parsed, ...current].slice(0, 8));
      if (['page-analyzed', 'fast-complete', 'audit-start', 'audit-complete', 'audit-error', 'complete', 'discovered'].includes(parsed.type)) {
        void loadScan(activeScan.id);
      }
      if (parsed.type === 'complete') {
        void refreshHistory();
        source.close();
      }
    };
    eventNames.forEach((name) => source.addEventListener(name, handleEvent));
    source.onerror = () => {
      source.close();
    };
    return () => {
      eventNames.forEach((name) => source.removeEventListener(name, handleEvent));
      source.close();
    };
  }, [activeScan?.id, activeScan?.status]);

  const selectedPage = useMemo(() => {
    if (!scan?.pages.length) {
      return null;
    }
    return scan.pages.find((page) => page.url === selectedPageURL) || scan.pages[0];
  }, [scan, selectedPageURL]);

  const filteredPages = useMemo(() => {
    const value = pageFilter.trim().toLowerCase();
    if (!scan) {
      return [];
    }
    if (!value) {
      return scan.pages;
    }
    return scan.pages.filter((page) =>
      [page.url, page.title, page.h1, page.description, page.auditError, page.fetchError]
        .filter((item): item is string => Boolean(item))
        .some((item) => item.toLowerCase().includes(value)),
    );
  }, [scan, pageFilter]);

  async function refreshHistory() {
    try {
      setHistory(await listScans());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to load history');
    }
  }

  async function loadScan(id: string) {
    try {
      const result = await getScan(id);
      setScan(result);
      setActiveScan(result.summary);
      setSelectedPageURL((current) => current || result.pages[0]?.url || null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to load scan');
    }
  }

  async function onStartScan(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    setEvents([]);
    setLoading(true);
    try {
      const { isEDS } = await checkEds(url);
      if (!isEDS) {
        setNotEdsOpen(true);
        return;
      }
      const created = await startScan(url, null);
      setActiveScan(created);
      setScan({ summary: created, pages: [], blocks: [], sections: [], links: emptyLinks, seo: emptySEO, generatedAt: new Date().toISOString() });
      setSelectedPageURL(null);
      setTab('overview');
      await refreshHistory();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to start scan');
    } finally {
      setLoading(false);
    }
  }

  async function onCancelScan() {
    if (!activeScan) {
      return;
    }
    await cancelScan(activeScan.id).catch((err) => setError(err instanceof Error ? err.message : 'Unable to cancel scan'));
  }

  async function onAuditAll() {
    if (!scan) {
      return;
    }
    setError('');
    setEvents([]);
    try {
      const updated = await reauditScan(scan.summary.id, 'all');
      const running = { ...updated, status: 'running', phase: 'auditing' };
      setActiveScan(running);
      setScan((current) => (current ? { ...current, summary: running } : current));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to start Lighthouse run');
    }
  }

  const summary = scan?.summary || activeScan;
  const isRunning = summary?.status === 'running';

  return (
    <div className="app">
      <header className="appbar">
        <div className="brand">
          <span className="brand-mark">
            <Globe2 size={20} strokeWidth={2.4} />
          </span>
          <span className="brand-text">
            <strong>EDS Analyser</strong>
            <span>Edge Delivery crawler</span>
          </span>
        </div>

        <form className="scanbar" onSubmit={onStartScan}>
          <label htmlFor="site-url" className="sr-only">EDS URL</label>
          <div className="scanbar-field">
            <Search size={18} className="scanbar-icon" aria-hidden />
            <input
              id="site-url"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder="Enter an EDS site URL to analyse…"
              disabled={loading || isRunning}
            />
            <button type="submit" className="btn btn-primary" disabled={loading || isRunning || !url.trim()} title="Start scan">
              {loading ? <Loader2 className="spin" size={17} /> : <Play size={16} strokeWidth={2.6} />}
              <span className="btn-label">Analyse</span>
            </button>
          </div>
        </form>

        <div className="appbar-status">
          {isRunning && (
            <button type="button" className="btn btn-ghost btn-danger" onClick={onCancelScan}>
              <StopCircle size={16} />
              <span className="btn-label">Cancel</span>
            </button>
          )}
          <div className={`status-pill ${summary?.status || 'idle'}`}>
            <span className={`status-dot ${isRunning ? 'pulse' : ''}`} aria-hidden />
            {summary?.status || 'idle'}
          </div>
        </div>
      </header>

      <nav className="tabrail" aria-label="Sections">
        {tabs.map((item) => {
          const Icon = item.icon;
          return (
            <button key={item.id} type="button" className={tab === item.id ? 'active' : ''} onClick={() => setTab(item.id)}>
              <Icon size={17} />
              {item.label}
            </button>
          );
        })}
      </nav>

      <main className="canvas">
        <section className="hero">
          <div className="hero-title">
            <p className="eyebrow">Site health</p>
            <h1>{summary ? readableHost(summary.rootUrl) : 'Ready to crawl'}</h1>
            {summary && <span className="hero-phase">{phaseLabel(summary)}</span>}
          </div>
          {summary && (
            <div className="hero-health">
              <ScoreBadge score={summary.scores.health} />
              <span>Health score</span>
            </div>
          )}
        </section>

        {error && (
          <div className="alert" role="alert">
            <OctagonX size={18} />
            {error}
          </div>
        )}

        {summary && (
          <section className="progress-band">
            <Metric label="Phase" value={phaseLabel(summary)} />
            <Metric label="Fast pages" value={`${summary.fastCompletedPages}/${summary.discoveredPages}`} />
            <Metric label="Failures" value={summary.failedPages.toString()} tone={summary.failedPages ? 'warn' : 'good'} />
            <Metric label="Audits" value={`${summary.auditCompletedPages}/${summary.auditQueuedPages}`} />
            <Metric label="Health" value={formatScore(summary.scores.health)} tone={scoreTone(summary.scores.health)} />
          </section>
        )}

        {events.length > 0 && (
          <section className="event-strip" aria-label="Live activity">
            {events.map((event) => (
              <span key={`${event.timestamp}-${event.type}-${event.pageUrl || ''}`}>
                <span className="event-dot" aria-hidden />
                {event.type.replace('-', ' ')}
                {event.pageUrl ? `: ${compactURL(event.pageUrl)}` : event.message ? `: ${event.message}` : ''}
              </span>
            ))}
          </section>
        )}

        {!scan && <EmptyState history={history} onOpen={(id) => void loadScan(id)} />}

        {scan && tab === 'overview' && <Overview scan={scan} onAuditAll={() => void onAuditAll()} auditBusy={isRunning} />}
        {scan && tab === 'pages' && (
          <PagesView
            pages={filteredPages}
            selectedPage={selectedPage}
            pageFilter={pageFilter}
            onFilter={setPageFilter}
            onSelect={setSelectedPageURL}
          />
        )}
        {scan && tab === 'blocks' && <BlocksView blocks={scan.blocks} sections={scan.sections} />}
        {scan && tab === 'links' && <LinksView scan={scan} />}
        {scan && tab === 'seo' && <SEOView scan={scan} />}
        {tab === 'history' && <HistoryView history={history} currentID={summary?.id} onOpen={(id) => void loadScan(id)} />}
      </main>

      {notEdsOpen && <NotEdsModal onClose={() => setNotEdsOpen(false)} />}
    </div>
  );
}

function NotEdsModal({ onClose }: { onClose: () => void }) {
  return (
    <div className="modal-overlay" role="presentation" onClick={onClose}>
      <div className="modal-card" role="dialog" aria-modal="true" aria-labelledby="not-eds-title" onClick={(event) => event.stopPropagation()}>
        <button type="button" className="modal-close" onClick={onClose} aria-label="Close">
          <X size={18} />
        </button>
        <div className="modal-icon">
          <TriangleAlert size={26} />
        </div>
        <h2 id="not-eds-title">Enter an EDS site</h2>
        <p>
          This doesn&apos;t look like an Edge Delivery Services site &mdash; we couldn&apos;t find
          <code>/scripts/aem.js</code> at that origin. Please enter a valid EDS site URL.
        </p>
        <button type="button" className="btn btn-dark modal-action" onClick={onClose}>
          Got it
        </button>
      </div>
    </div>
  );
}

function Overview({ scan, onAuditAll, auditBusy }: { scan: ScanResult; onAuditAll: () => void; auditBusy: boolean }) {
  const totals = [
    { label: 'Pages analyzed', value: scan.summary.fastCompletedPages || scan.pages.length },
    { label: 'Blocks used', value: scan.pages.reduce((sum, page) => sum + page.blockCount, 0) },
    { label: 'Block types', value: scan.blocks.length },
    { label: 'Section variations', value: scan.sections.length },
    { label: 'Links', value: scan.links.total },
    { label: 'Missing OG images', value: scan.seo.missingOgImage },
  ];
  return (
    <section className="content-grid">
      <div className="panel">
        <div className="panel-heading">
          <h2>Fast EDS report</h2>
          <Boxes size={19} />
        </div>
        <div className="stat-list">
          {totals.map((item) => (
            <Metric key={item.label} label={item.label} value={item.value.toString()} />
          ))}
        </div>
      </div>
      <div className="panel wide">
        <div className="panel-heading">
          <h2>Lighthouse</h2>
          <BarChart3 size={19} />
        </div>
        <p className="panel-note">{lighthouseLabel(scan.summary)}</p>
        <button
          type="button"
          className="btn btn-dark"
          onClick={onAuditAll}
          disabled={auditBusy || scan.pages.length === 0}
          title="Run Lighthouse on every page in this scan"
        >
          {auditBusy ? <Loader2 className="spin" size={16} /> : <BarChart3 size={16} />}
          Run Lighthouse for all pages
        </button>
        <div className="score-grid">
          <ScoreGauge label="Performance" score={scan.summary.scores.performance} />
          <ScoreGauge label="Accessibility" score={scan.summary.scores.accessibility} />
          <ScoreGauge label="Best Practices" score={scan.summary.scores.bestPractices} />
          <ScoreGauge label="SEO" score={scan.summary.scores.seo} />
        </div>
      </div>
    </section>
  );
}

function PagesView({
  pages,
  selectedPage,
  pageFilter,
  onFilter,
  onSelect,
}: {
  pages: PageResult[];
  selectedPage: PageResult | null;
  pageFilter: string;
  onFilter: (value: string) => void;
  onSelect: (value: string) => void;
}) {
  return (
    <section className="pages-layout">
      <div className="panel table-panel">
        <div className="panel-heading">
          <h2>Pages</h2>
          <div className="search-field">
            <Search size={16} />
            <input value={pageFilter} onChange={(event) => onFilter(event.target.value)} placeholder="Filter pages" />
          </div>
        </div>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>URL</th>
                <th>Title</th>
                <th>Health</th>
                <th>Audit</th>
                <th>Blocks</th>
                <th>Links</th>
              </tr>
            </thead>
            <tbody>
              {pages.map((page) => (
                <tr key={page.url} onClick={() => onSelect(page.url)} className={selectedPage?.url === page.url ? 'selected' : ''}>
                  <td>{compactURL(page.url)}</td>
                  <td>{page.title || 'Missing title'}</td>
                  <td><ScoreBadge score={page.lighthouse.health} /></td>
                  <td><span className={`audit-status ${page.auditStatus}`}>{page.auditStatus}</span></td>
                  <td>{page.blockCount}</td>
                  <td>{page.linkCount}</td>
                </tr>
              ))}
              {pages.length === 0 && <EmptyTableRow columns={6} message="No pages analyzed yet" />}
            </tbody>
          </table>
        </div>
      </div>
      <PageDetail page={selectedPage} />
    </section>
  );
}

function PageDetail({ page }: { page: PageResult | null }) {
  if (!page) {
    return <div className="panel detail-panel"><h2>Page detail</h2></div>;
  }
  return (
    <div className="panel detail-panel">
      <div className="panel-heading">
        <h2>Page detail</h2>
        <a href={page.url} target="_blank" rel="noreferrer" className="icon-link" title="Open page">
          <ExternalLink size={17} />
        </a>
      </div>
      <dl className="detail-list">
        <dt>Title</dt><dd>{page.title || 'Missing'}</dd>
        <dt>H1</dt><dd>{page.h1 || 'Missing'}</dd>
        <dt>Status</dt><dd>{page.statusCode || 'n/a'}</dd>
        <dt>Audit</dt><dd><span className={`audit-status ${page.auditStatus}`}>{page.auditStatus}</span></dd>
        <dt>Canonical</dt><dd>{page.canonical || 'Missing'}</dd>
        <dt>Description</dt><dd>{page.description || 'Missing'}</dd>
        <dt>OG title</dt><dd>{page.og.title || 'Missing'}</dd>
        <dt>OG image</dt><dd>{page.og.image || 'Missing'}</dd>
      </dl>
      <div className="mini-grid">
        <Metric label="Sections" value={page.sectionCount.toString()} />
        <Metric label="Blocks" value={page.blockCount.toString()} />
        <Metric label="Internal" value={page.internalLinks.toString()} />
        <Metric label="External" value={page.externalLinks.toString()} />
      </div>
      {(page.fetchError || page.auditError) && (
        <div className="warning-box">
          {page.fetchError || page.auditError}
        </div>
      )}
      <h3>Blocks</h3>
      <div className="chip-row">
        {page.blocks.map((block, index) => (
          <span key={`${block.name}-${index}`} className="chip">{block.name}{block.variations.length ? ` / ${block.variations.join(', ')}` : ''}</span>
        ))}
        {page.blocks.length === 0 && <span className="muted-text">No blocks found</span>}
      </div>
      <h3>Links</h3>
      <div className="link-list">
        {page.links.slice(0, 20).map((link, index) => (
          <a key={`${link.url}-${index}`} href={link.url} target="_blank" rel="noreferrer">
            <span>{link.kind}</span>
            {link.text || compactURL(link.url)}
          </a>
        ))}
        {page.links.length === 0 && <span className="muted-text">No links found</span>}
      </div>
    </div>
  );
}

function BlocksView({ blocks, sections }: { blocks: BlockStat[]; sections: SectionStat[] }) {
  return (
    <section className="content-grid">
      <StatTable
        title="Blocks"
        rows={blocks.map((block) => ({
          name: block.name,
          count: block.count,
          detail: Object.entries(block.variations).map(([name, count]) => `${name} ${count}`).join(', ') || 'base',
        }))}
      />
      <StatTable
        title="Section variations"
        rows={sections.map((section) => ({
          name: section.variation,
          count: section.count,
          detail: `${section.pages.length} pages`,
        }))}
      />
    </section>
  );
}

function LinksView({ scan }: { scan: ScanResult }) {
  const allLinks = scan.pages.flatMap((page) => page.links);
  return (
    <section className="content-grid">
      <div className="panel">
        <div className="panel-heading"><h2>Links</h2><Link2 size={19} /></div>
        <div className="stat-list">
          <Metric label="Total" value={scan.links.total.toString()} />
          <Metric label="Internal" value={scan.links.internal.toString()} />
          <Metric label="External" value={scan.links.external.toString()} />
          <Metric label="Assets" value={scan.links.asset.toString()} />
          <Metric label="Unique internal" value={scan.links.uniqueInternal.toString()} />
          <Metric label="Unique external" value={scan.links.uniqueExternal.toString()} />
        </div>
      </div>
      <div className="panel wide table-panel">
        <div className="panel-heading"><h2>All links</h2></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Kind</th><th>Text</th><th>URL</th><th>Page</th></tr></thead>
            <tbody>
              {allLinks.slice(0, 250).map((link, index) => (
                <tr key={`${link.url}-${index}`}>
                  <td>{link.kind}</td>
                  <td>{link.text || '-'}</td>
                  <td>{compactURL(link.url)}</td>
                  <td>{link.pageUrl ? compactURL(link.pageUrl) : '-'}</td>
                </tr>
              ))}
              {allLinks.length === 0 && <EmptyTableRow columns={4} message="No links found yet" />}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function SEOView({ scan }: { scan: ScanResult }) {
  return (
    <section className="content-grid">
      <div className="panel">
        <div className="panel-heading"><h2>SEO gaps</h2><ShieldCheck size={19} /></div>
        <div className="stat-list">
          <Metric label="Missing title" value={scan.seo.missingTitle.toString()} tone={scan.seo.missingTitle ? 'warn' : 'good'} />
          <Metric label="Missing description" value={scan.seo.missingDescription.toString()} tone={scan.seo.missingDescription ? 'warn' : 'good'} />
          <Metric label="Missing H1" value={scan.seo.missingH1.toString()} tone={scan.seo.missingH1 ? 'warn' : 'good'} />
          <Metric label="Missing canonical" value={scan.seo.missingCanonical.toString()} tone={scan.seo.missingCanonical ? 'warn' : 'good'} />
          <Metric label="Missing OG title" value={scan.seo.missingOgTitle.toString()} tone={scan.seo.missingOgTitle ? 'warn' : 'good'} />
          <Metric label="Missing OG image" value={scan.seo.missingOgImage.toString()} tone={scan.seo.missingOgImage ? 'warn' : 'good'} />
        </div>
      </div>
      <div className="panel wide table-panel">
        <div className="panel-heading"><h2>Open Graph</h2></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Page</th><th>OG title</th><th>OG image</th><th>OG URL</th></tr></thead>
            <tbody>
              {scan.pages.map((page) => (
                <tr key={page.url}>
                  <td>{compactURL(page.url)}</td>
                  <td>{page.og.title || 'Missing'}</td>
                  <td>{page.og.image ? compactURL(page.og.image) : 'Missing'}</td>
                  <td>{page.og.url ? compactURL(page.og.url) : 'Missing'}</td>
                </tr>
              ))}
              {scan.pages.length === 0 && <EmptyTableRow columns={4} message="No pages analyzed yet" />}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function HistoryView({ history, currentID, onOpen }: { history: ScanSummary[]; currentID?: string; onOpen: (id: string) => void }) {
  return (
    <section className="panel table-panel">
      <div className="panel-heading"><h2>History</h2><History size={19} /></div>
      <div className="table-scroll">
        <table>
          <thead><tr><th>Site</th><th>Phase</th><th>Pages</th><th>Audits</th><th>Health</th><th>Started</th></tr></thead>
          <tbody>
            {history.map((item) => (
              <tr key={item.id} onClick={() => onOpen(item.id)} className={item.id === currentID ? 'selected' : ''}>
                <td>{readableHost(item.rootUrl)}</td>
                <td>{phaseLabel(item)}</td>
                <td>{item.fastCompletedPages}/{item.discoveredPages}</td>
                <td>{item.auditCompletedPages}/{item.auditQueuedPages}</td>
                <td><ScoreBadge score={item.scores.health} /></td>
                <td>{new Date(item.startedAt).toLocaleString()}</td>
              </tr>
            ))}
            {history.length === 0 && <EmptyTableRow columns={6} message="No scans yet" />}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function EmptyState({ history, onOpen }: { history: ScanSummary[]; onOpen: (id: string) => void }) {
  return (
    <section className="empty-state">
      <div className="empty-hero">
        <span className="empty-icon">
          <FileSearch size={30} strokeWidth={1.8} />
        </span>
        <h2>No scan selected</h2>
        <p>
          Paste an Edge Delivery Services URL in the bar above and hit Analyse. We&apos;ll crawl the site,
          map its blocks, links and SEO, then run Lighthouse audits.
        </p>
      </div>
      {history.length > 0 && (
        <div className="empty-recent">
          <p className="eyebrow">Recent scans</p>
          <div className="recent-grid">
            {history.slice(0, 4).map((item) => (
              <button key={item.id} type="button" className="recent-card" onClick={() => onOpen(item.id)}>
                <span className="recent-host">{readableHost(item.rootUrl)}</span>
                <span className="recent-meta">{item.fastCompletedPages}/{item.discoveredPages} pages</span>
                <ScoreBadge score={item.scores.health} />
              </button>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function StatTable({ title, rows }: { title: string; rows: Array<{ name: string; count: number; detail: string }> }) {
  return (
    <div className="panel table-panel">
      <div className="panel-heading"><h2>{title}</h2></div>
      <div className="table-scroll">
        <table>
          <thead><tr><th>Name</th><th>Count</th><th>Detail</th></tr></thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.name}>
                <td>{row.name}</td>
                <td>{row.count}</td>
                <td>{row.detail}</td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyTableRow columns={3} message="No data yet" />}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function EmptyTableRow({ columns, message }: { columns: number; message: string }) {
  return (
    <tr>
      <td colSpan={columns} className="empty-cell">{message}</td>
    </tr>
  );
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: 'good' | 'warn' | 'bad' | 'muted' }) {
  return (
    <div className={`metric ${tone || ''}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function ScoreGauge({ label, score }: { label: string; score: number | null }) {
  const value = Math.max(0, Math.min(100, score || 0));
  const tone = scoreTone(score);
  return (
    <div className={`score-gauge ${tone}`}>
      <div className="gauge-ring" style={{ ['--value' as string]: value } as React.CSSProperties}>
        <span className="gauge-track" aria-hidden />
        <strong>{formatScore(score)}</strong>
      </div>
      <span className="gauge-label">{label}</span>
    </div>
  );
}

function ScoreBadge({ score }: { score: number | null }) {
  return <span className={`score-badge ${scoreTone(score)}`}>{formatScore(score)}</span>;
}

function phaseLabel(summary: ScanSummary) {
  switch (summary.phase || summary.status) {
    case 'discovering':
      return 'Discovering';
    case 'analyzing':
      return 'Analyzing pages';
    case 'fast-complete':
      return 'Fast report ready';
    case 'auditing':
      return summary.auditQueuedPages > 0 ? `Auditing ${summary.auditQueuedPages} pages` : 'Auditing';
    case 'completed':
      return 'Complete';
    case 'cancelled':
      return 'Cancelled';
    default:
      return summary.status || 'Idle';
  }
}

function lighthouseLabel(summary: ScanSummary) {
  if (summary.auditQueuedPages === 0 && summary.phase !== 'completed') {
    return 'Lighthouse starts after the fast report is ready.';
  }
  if (summary.phase === 'auditing') {
    return `Auditing ${summary.auditCompletedPages}/${summary.auditQueuedPages} pages.`;
  }
  if (summary.auditQueuedPages > 0) {
    return `Audited ${summary.auditCompletedPages}/${summary.auditQueuedPages} pages.`;
  }
  return 'No Lighthouse audits queued.';
}

function formatScore(score: number | null | undefined) {
  return typeof score === 'number' ? Math.round(score).toString() : '-';
}

function scoreTone(score: number | null | undefined): 'good' | 'warn' | 'bad' | 'muted' {
  if (typeof score !== 'number') {
    return 'muted';
  }
  if (score >= 90) {
    return 'good';
  }
  if (score >= 50) {
    return 'warn';
  }
  return 'bad';
}

function readableHost(raw: string) {
  try {
    return new URL(raw).host;
  } catch {
    return raw;
  }
}

function compactURL(raw: string) {
  try {
    const parsed = new URL(raw);
    return `${parsed.host}${parsed.pathname === '/' ? '' : parsed.pathname}`;
  } catch {
    return raw;
  }
}

const emptyLinks = { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0 };
const emptySEO = { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 };
