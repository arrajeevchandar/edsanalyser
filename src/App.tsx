import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertCircle,
  BarChart3,
  Boxes,
  CheckCircle2,
  ClipboardList,
  ExternalLink,
  FileSearch,
  Globe2,
  History,
  Layers3,
  Link2,
  Loader2,
  PanelTop,
  OctagonX,
  Play,
  Route,
  Search,
  ShieldCheck,
  Sparkles,
  StopCircle,
  TrendingUp,
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
              placeholder="Enter an EDS site URL to analyse..."
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
            <p className="eyebrow">EDS site intelligence</p>
            <h1>{summary ? readableHost(summary.rootUrl) : 'Ready to crawl'}</h1>
            <p className="hero-copy">
              {summary
                ? `${phaseLabel(summary)} - fast source analysis, SEO checks, link inventory, and Lighthouse follow-up in one workspace.`
                : 'Paste an Edge Delivery Services URL to map pages, blocks, links, SEO, Open Graph tags, and Lighthouse health.'}
            </p>
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
          <section className="progress-band compact">
            <Metric label="Phase" value={phaseLabel(summary)} />
            <Metric label="Fast pages" value={`${summary.fastCompletedPages}/${summary.discoveredPages}`} />
            <Metric label="Fetch failures" value={summary.failedPages.toString()} tone={summary.failedPages ? 'warn' : 'good'} />
            <Metric label="Lighthouse" value={`${summary.auditCompletedPages}/${summary.auditQueuedPages}`} />
            <Metric label="Audit errors" value={summary.auditFailedPages.toString()} tone={summary.auditFailedPages ? 'warn' : 'good'} />
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
          This doesn&apos;t look like an Edge Delivery Services site - we couldn&apos;t find
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
  const totalBlocks = scan.pages.reduce((sum, page) => sum + page.blockCount, 0);
  const totalSections = scan.pages.reduce((sum, page) => sum + page.sectionCount, 0);
  const seoGaps = totalSEOGaps(scan);
  const pagesWithIssues = scan.pages.filter((page) => pageIssueCount(page) > 0).length;
  const topBlocks = scan.blocks.slice(0, 5);
  const topPages = [...scan.pages].sort((a, b) => pageIssueCount(b) - pageIssueCount(a)).slice(0, 4);
  const insightCards = [
    {
      label: 'Fast coverage',
      value: `${scan.summary.fastCompletedPages || scan.pages.length}/${scan.summary.discoveredPages || scan.pages.length}`,
      detail: 'Pages with source HTML analysed',
      tone: 'good' as const,
      icon: <FileSearch size={18} />,
    },
    {
      label: 'Content model',
      value: `${scan.blocks.length}`,
      detail: `${totalBlocks} blocks across ${totalSections} sections`,
      tone: 'muted' as const,
      icon: <Layers3 size={18} />,
    },
    {
      label: 'SEO attention',
      value: seoGaps.toString(),
      detail: `${pagesWithIssues} pages with missing metadata`,
      tone: seoGaps ? 'warn' as const : 'good' as const,
      icon: seoGaps ? <AlertCircle size={18} /> : <CheckCircle2 size={18} />,
    },
    {
      label: 'Link surface',
      value: scan.links.total.toString(),
      detail: `${scan.links.internal} internal, ${scan.links.external} external`,
      tone: 'muted' as const,
      icon: <Route size={18} />,
    },
  ];

  return (
    <section className="overview-stack">
      <section className="insight-hero-panel">
        <div className="insight-narrative">
          <p className="eyebrow">Executive readout</p>
          <h2>{siteReadout(scan)}</h2>
          <p>
            The fast report is designed for authors: surface metadata gaps, content reuse,
            link shape, and Lighthouse status before the deeper inventory tables.
          </p>
        </div>
        <div className="insight-card-grid">
          {insightCards.map((item) => (
            <InsightCard key={item.label} {...item} />
          ))}
        </div>
      </section>

      <section className="overview-grid">
        <div className="panel priority-panel">
          <div className="panel-heading">
            <h2>Priority checks</h2>
            <ClipboardList size={19} />
          </div>
          <div className="issue-stack">
            {priorityIssues(scan).map((issue) => (
              <IssueItem key={issue.label} {...issue} />
            ))}
          </div>
        </div>

        <div className="panel">
          <div className="panel-heading">
            <h2>Content shape</h2>
            <Boxes size={19} />
          </div>
          <div className="compact-list">
            {topBlocks.map((block) => (
              <div key={block.name} className="rank-row">
                <span>{block.name}</span>
                <strong>{block.count}</strong>
              </div>
            ))}
            {topBlocks.length === 0 && <EmptyInline message="No blocks found yet" />}
          </div>
          <div className="mini-grid">
            <Metric label="Block types" value={scan.blocks.length.toString()} />
            <Metric label="Section variations" value={scan.sections.length.toString()} />
          </div>
        </div>

        <div className="panel">
          <div className="panel-heading">
            <h2>Link mix</h2>
            <Link2 size={19} />
          </div>
          <DistributionBar
            items={[
              { label: 'Internal', value: scan.links.internal, tone: 'dark' },
              { label: 'External', value: scan.links.external, tone: 'red' },
              { label: 'Assets', value: scan.links.asset, tone: 'light' },
            ]}
          />
          <div className="mini-grid">
            <Metric label="Unique internal" value={scan.links.uniqueInternal.toString()} />
            <Metric label="Unique external" value={scan.links.uniqueExternal.toString()} />
          </div>
        </div>

        <div className="panel priority-panel">
          <div className="panel-heading">
            <h2>Pages to review</h2>
            <PanelTop size={19} />
          </div>
          <div className="compact-list">
            {topPages.map((page) => (
              <div key={page.url} className="page-signal">
                <span>{compactURL(page.url)}</span>
                <strong>{pageIssueCount(page)} issues</strong>
              </div>
            ))}
            {topPages.length === 0 && <EmptyInline message="No pages analysed yet" />}
          </div>
        </div>
      </section>

      <section className="panel lighthouse-panel">
        <div className="panel-heading">
          <h2>Lighthouse</h2>
          <BarChart3 size={19} />
        </div>
        <div className="lighthouse-content">
          <div>
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
          </div>
          <div className="score-grid">
            <ScoreGauge label="Performance" score={scan.summary.scores.performance} />
            <ScoreGauge label="Accessibility" score={scan.summary.scores.accessibility} />
            <ScoreGauge label="Best Practices" score={scan.summary.scores.bestPractices} />
            <ScoreGauge label="SEO" score={scan.summary.scores.seo} />
          </div>
        </div>
      </section>
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
  const missingTitles = pages.filter((page) => !page.title).length;
  const missingDescriptions = pages.filter((page) => !page.description).length;
  const auditComplete = pages.filter((page) => page.auditStatus === 'complete').length;
  const fetchFailures = pages.filter((page) => page.fetchError).length;

  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">Page inventory</p>
          <h2>Find weak pages quickly</h2>
        </div>
        <div className="search-field">
          <Search size={16} />
          <input value={pageFilter} onChange={(event) => onFilter(event.target.value)} placeholder="Filter pages" />
        </div>
      </div>

      <div className="summary-ribbon">
        <Metric label="Visible pages" value={pages.length.toString()} />
        <Metric label="Missing titles" value={missingTitles.toString()} tone={missingTitles ? 'warn' : 'good'} />
        <Metric label="Missing descriptions" value={missingDescriptions.toString()} tone={missingDescriptions ? 'warn' : 'good'} />
        <Metric label="Audited" value={auditComplete.toString()} />
        <Metric label="Fetch failures" value={fetchFailures.toString()} tone={fetchFailures ? 'bad' : 'good'} />
      </div>

      <section className="pages-layout">
        <div className="panel table-panel">
          <div className="panel-heading">
            <h2>Pages</h2>
            <span className="panel-count">{pages.length} shown</span>
          </div>
          <div className="table-scroll page-table-scroll">
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
                    <td>
                      <span className="url-cell">{compactURL(page.url)}</span>
                      {page.fetchError && <span className="row-warning">Fetch failed</span>}
                    </td>
                    <td>{page.title || 'Missing title'}</td>
                    <td><ScoreBadge score={page.lighthouse.health} /></td>
                    <td><span className={`audit-status ${page.auditStatus}`}>{page.auditStatus || 'pending'}</span></td>
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
    </section>
  );
}

function PageDetail({ page }: { page: PageResult | null }) {
  if (!page) {
    return (
      <div className="panel detail-panel page-inspector">
        <h2>Page detail</h2>
        <EmptyInline message="Select a page to inspect metadata, blocks, links, and Lighthouse status." />
      </div>
    );
  }
  const issues = pageIssues(page);

  return (
    <div className="panel detail-panel page-inspector">
      <div className="inspector-head">
        <div>
          <p className="eyebrow">Selected page</p>
          <h2>{page.title || 'Missing title'}</h2>
          <span>{compactURL(page.url)}</span>
        </div>
        <a href={page.url} target="_blank" rel="noreferrer" className="icon-link" title="Open page">
          <ExternalLink size={17} />
        </a>
      </div>

      <div className="inspector-status">
        <ScoreGauge label="Health" score={page.lighthouse.health} />
        <div className="status-stack">
          <span className={`audit-status ${page.auditStatus}`}>{page.auditStatus || 'pending'}</span>
          <span>Status {page.statusCode || 'n/a'}</span>
          <span>{page.sectionCount} sections / {page.blockCount} blocks / {page.linkCount} links</span>
        </div>
      </div>

      <div className="metadata-grid">
        <MetaItem label="H1" value={page.h1} />
        <MetaItem label="Description" value={page.description} />
        <MetaItem label="Canonical" value={page.canonical} />
        <MetaItem label="Language" value={page.lang} />
        <MetaItem label="Robots" value={page.robots} />
        <MetaItem label="OG title" value={page.og.title} />
        <MetaItem label="OG image" value={page.og.image} />
        <MetaItem label="OG URL" value={page.og.url} />
      </div>

      <div className="issue-stack tight">
        {issues.map((issue) => (
          <IssueItem key={issue.label} {...issue} />
        ))}
        {issues.length === 0 && <IssueItem label="Page metadata looks complete" detail="Title, H1, description, canonical, and key OG fields are present." tone="good" />}
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
      <div className="link-list compact">
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
  const [selectedBlockName, setSelectedBlockName] = useState<string | null>(null);
  const totalBlocks = blocks.reduce((sum, block) => sum + block.count, 0);
  const totalVariations = blocks.reduce((sum, block) => sum + Object.keys(block.variations).length, 0);
  const broadestBlock = blocks.reduce<BlockStat | null>((best, block) => {
    if (!best || block.pages.length > best.pages.length) {
      return block;
    }
    return best;
  }, null);
  const selectedBlock = blocks.find((block) => block.name === selectedBlockName) || blocks[0] || null;

  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">Content model</p>
          <h2>Blocks and section patterns</h2>
        </div>
      </div>

      <div className="summary-ribbon">
        <Metric label="Total block usage" value={totalBlocks.toString()} />
        <Metric label="Block types" value={blocks.length.toString()} />
        <Metric label="Block variations" value={totalVariations.toString()} />
        <Metric label="Section variations" value={sections.length.toString()} />
        <Metric label="Broadest block" value={broadestBlock?.name || '-'} />
      </div>

      <section className="split-workspace blocks-workspace">
        <div className="panel pattern-panel compact-side-panel">
          <div className="panel-heading">
            <h2>Blocks</h2>
            <span className="panel-count">{blocks.length}</span>
          </div>
          <div className="select-list">
            {blocks.map((block) => (
              <button
                key={block.name}
                type="button"
                className={selectedBlock?.name === block.name ? 'active' : ''}
                onClick={() => setSelectedBlockName(block.name)}
              >
                <span>
                  <strong>{block.name}</strong>
                  <small>{Object.keys(block.variations).length || 'Base'} variations</small>
                </span>
                <b>{block.count}</b>
              </button>
            ))}
            {blocks.length === 0 && <EmptyInline message="No data yet" />}
          </div>
        </div>
        <BlockDetail block={selectedBlock} sections={sections} />
      </section>
    </section>
  );
}

function BlockDetail({ block, sections }: { block: BlockStat | null; sections: SectionStat[] }) {
  const variations = block ? Object.entries(block.variations).sort((a, b) => b[1] - a[1]) : [];
  const sectionRows = sections.slice(0, 12);

  if (!block) {
    return (
      <div className="panel block-detail-panel">
        <EmptyInline message="No data yet" />
      </div>
    );
  }

  return (
    <div className="panel block-detail-panel">
      <div className="block-detail-head">
        <div>
          <p className="eyebrow">Block detail</p>
          <h2>{block.name}</h2>
          <p>{block.count} total uses across {block.pages.length} pages.</p>
        </div>
        <span className="block-count-pill">{block.count}</span>
      </div>

      <div className="detail-metric-grid">
        <Metric label="Total usage" value={block.count.toString()} />
        <Metric label="Pages using it" value={block.pages.length.toString()} />
        <Metric label="Variations" value={variations.length.toString()} />
        <Metric label="Base usage" value={variations.length ? 'Mixed' : 'Base'} />
      </div>

      <section className="detail-columns">
        <div>
          <div className="panel-subhead">
            <h3>Variation usage</h3>
            <TrendingUp size={16} />
          </div>
          <div className="compact-list">
            {variations.map(([name, count]) => (
              <div key={name} className="rank-row">
                <span>{name}</span>
                <strong>{count}</strong>
              </div>
            ))}
            {variations.length === 0 && <EmptyInline message="Only the base block style was found." />}
          </div>
        </div>

        <div>
          <div className="panel-subhead">
            <h3>Pages using this block</h3>
            <PanelTop size={16} />
          </div>
          <div className="page-chip-list">
            {block.pages.map((page) => (
              <a key={page} href={page} target="_blank" rel="noreferrer">{compactURL(page)}</a>
            ))}
            {block.pages.length === 0 && <EmptyInline message="No page usage recorded." />}
          </div>
        </div>
      </section>

      <section className="section-strip">
        <div className="panel-subhead">
          <h3>Section variation context</h3>
          <Layers3 size={16} />
        </div>
        <div className="section-variation-grid">
          {sectionRows.map((section) => (
            <div key={section.variation} className="section-variation-card">
              <span>{section.variation}</span>
              <strong>{section.count}</strong>
              <small>{section.pages.length} pages</small>
            </div>
          ))}
          {sectionRows.length === 0 && <EmptyInline message="No section variations found yet." />}
        </div>
      </section>
    </div>
  );
}

function LinksView({ scan }: { scan: ScanResult }) {
  const allLinks = scan.pages.flatMap((page) => page.links);
  const brokenLinks = allLinks.filter((link) => typeof link.status === 'number' && link.status >= 400);
  const unlabeledLinks = allLinks.filter((link) => !link.text && link.kind !== 'asset');

  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">Link graph</p>
          <h2>Understand where authors send readers</h2>
        </div>
      </div>

      <div className="summary-ribbon">
        <Metric label="Total links" value={scan.links.total.toString()} />
        <Metric label="Internal" value={scan.links.internal.toString()} />
        <Metric label="External" value={scan.links.external.toString()} />
        <Metric label="Assets" value={scan.links.asset.toString()} />
        <Metric label="Broken known" value={brokenLinks.length.toString()} tone={brokenLinks.length ? 'bad' : 'good'} />
      </div>

      <section className="split-workspace">
        <div className="panel compact-side-panel">
          <div className="panel-heading"><h2>Distribution</h2><Link2 size={19} /></div>
          <DistributionBar
            items={[
              { label: 'Internal', value: scan.links.internal, tone: 'dark' },
              { label: 'External', value: scan.links.external, tone: 'red' },
              { label: 'Assets', value: scan.links.asset, tone: 'light' },
              { label: 'Mail/tel/hash', value: scan.links.mail + scan.links.tel + scan.links.hash, tone: 'soft' },
            ]}
          />
          <div className="issue-stack tight">
            <IssueItem
              label="Unique destinations"
              detail={`${scan.links.uniqueInternal} internal and ${scan.links.uniqueExternal} external unique URLs.`}
              tone="muted"
            />
            <IssueItem
              label="Unlabeled anchors"
              detail={`${unlabeledLinks.length} links have no visible anchor text.`}
              tone={unlabeledLinks.length ? 'warn' : 'good'}
            />
          </div>
        </div>
        <div className="panel wide table-panel">
          <div className="panel-heading">
            <h2>All links</h2>
            <span className="panel-count">Showing first 250</span>
          </div>
          <div className="table-scroll no-x">
            <table className="fit-table links-table">
              <thead><tr><th>Kind</th><th>Text</th><th>URL</th><th>Page</th></tr></thead>
              <tbody>
                {allLinks.slice(0, 250).map((link, index) => (
                  <tr key={`${link.url}-${index}`}>
                    <td><span className={`link-kind ${link.kind}`}>{link.kind}</span></td>
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
    </section>
  );
}

function SEOView({ scan }: { scan: ScanResult }) {
  const seoIssues = [
    { label: 'Missing title', count: scan.seo.missingTitle },
    { label: 'Missing description', count: scan.seo.missingDescription },
    { label: 'Missing H1', count: scan.seo.missingH1 },
    { label: 'Missing canonical', count: scan.seo.missingCanonical },
    { label: 'Missing OG title', count: scan.seo.missingOgTitle },
    { label: 'Missing OG image', count: scan.seo.missingOgImage },
    { label: 'Missing OG URL', count: scan.seo.missingOgUrl },
  ];
  const cleanFields = seoIssues.filter((issue) => issue.count === 0).length;
  const pagesWithCompleteOG = scan.pages.filter((page) => page.og.title && page.og.image && page.og.url).length;

  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">SEO and sharing</p>
          <h2>Metadata completeness</h2>
        </div>
      </div>

      <div className="summary-ribbon">
        <Metric label="SEO checks clear" value={`${cleanFields}/7`} tone={cleanFields === 7 ? 'good' : 'warn'} />
        <Metric label="Total gaps" value={totalSEOGaps(scan).toString()} tone={totalSEOGaps(scan) ? 'warn' : 'good'} />
        <Metric label="Complete OG pages" value={`${pagesWithCompleteOG}/${scan.pages.length}`} />
        <Metric label="Missing OG image" value={scan.seo.missingOgImage.toString()} tone={scan.seo.missingOgImage ? 'warn' : 'good'} />
        <Metric label="Missing title" value={scan.seo.missingTitle.toString()} tone={scan.seo.missingTitle ? 'warn' : 'good'} />
      </div>

      <section className="split-workspace">
        <div className="panel seo-checklist compact-side-panel">
          <div className="panel-heading"><h2>SEO gaps</h2><ShieldCheck size={19} /></div>
          <div className="issue-stack">
            {seoIssues.map((issue) => (
              <IssueItem
                key={issue.label}
                label={issue.label}
                detail={issue.count ? `${issue.count} pages need attention.` : 'No gaps found.'}
                tone={issue.count ? 'warn' : 'good'}
              />
            ))}
          </div>
        </div>
        <div className="panel wide table-panel">
          <div className="panel-heading"><h2>Open Graph</h2><span className="panel-count">{scan.pages.length} pages</span></div>
          <div className="table-scroll no-x">
            <table className="fit-table seo-table">
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
    </section>
  );
}

function HistoryView({ history, currentID, onOpen }: { history: ScanSummary[]; currentID?: string; onOpen: (id: string) => void }) {
  const completed = history.filter((item) => item.status === 'completed').length;
  const running = history.filter((item) => item.status === 'running').length;

  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">Scan archive</p>
          <h2>History</h2>
        </div>
      </div>

      <div className="summary-ribbon">
        <Metric label="Saved scans" value={history.length.toString()} />
        <Metric label="Completed" value={completed.toString()} />
        <Metric label="Running" value={running.toString()} tone={running ? 'warn' : 'good'} />
        <Metric label="Selected" value={currentID ? '1' : '0'} />
      </div>

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

function EmptyInline({ message }: { message: string }) {
  return (
    <div className="empty-inline">
      <Sparkles size={18} />
      <span>{message}</span>
    </div>
  );
}

function InsightCard({
  label,
  value,
  detail,
  tone,
  icon,
}: {
  label: string;
  value: string;
  detail: string;
  tone: 'good' | 'warn' | 'bad' | 'muted';
  icon: React.ReactNode;
}) {
  return (
    <div className={`insight-card ${tone}`}>
      <span className="insight-icon">{icon}</span>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}

function IssueItem({ label, detail, tone }: { label: string; detail: string; tone: 'good' | 'warn' | 'bad' | 'muted' }) {
  const Icon = tone === 'good' ? CheckCircle2 : tone === 'bad' ? OctagonX : tone === 'warn' ? AlertCircle : Activity;
  return (
    <div className={`issue-item ${tone}`}>
      <span className="issue-icon"><Icon size={16} /></span>
      <span>
        <strong>{label}</strong>
        <small>{detail}</small>
      </span>
    </div>
  );
}

function DistributionBar({
  items,
}: {
  items: Array<{ label: string; value: number; tone: 'dark' | 'red' | 'light' | 'soft' }>;
}) {
  const total = items.reduce((sum, item) => sum + item.value, 0);
  return (
    <div className="distribution">
      <div className="distribution-bar" aria-label="Link distribution">
        {items.map((item) => (
          <span
            key={item.label}
            className={`distribution-segment ${item.tone}`}
            style={{ width: `${total ? Math.max((item.value / total) * 100, item.value > 0 ? 5 : 0) : 0}%` }}
          />
        ))}
      </div>
      <div className="distribution-legend">
        {items.map((item) => (
          <span key={item.label}>
            <i className={`legend-dot ${item.tone}`} />
            {item.label}: {item.value}
          </span>
        ))}
      </div>
    </div>
  );
}

function MetaItem({ label, value }: { label: string; value: string }) {
  return (
    <div className={`meta-item ${value ? '' : 'missing'}`}>
      <span>{label}</span>
      <strong>{value || 'Missing'}</strong>
    </div>
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

function siteReadout(scan: ScanResult) {
  const gaps = totalSEOGaps(scan);
  if (scan.summary.status === 'running') {
    return 'Fast analysis is filling in while Lighthouse runs in the background.';
  }
  if (scan.summary.failedPages > 0) {
    return `${scan.summary.failedPages} pages could not be fetched and need a crawl check.`;
  }
  if (gaps > 0) {
    return `${gaps} SEO or Open Graph gaps are the clearest action items.`;
  }
  if (scan.summary.scores.health !== null) {
    return `Lighthouse health is ${formatScore(scan.summary.scores.health)} across audited pages.`;
  }
  return 'The fast EDS report is ready for review.';
}

function totalSEOGaps(scan: ScanResult) {
  return (
    scan.seo.missingTitle +
    scan.seo.missingDescription +
    scan.seo.missingH1 +
    scan.seo.missingCanonical +
    scan.seo.missingOgTitle +
    scan.seo.missingOgImage +
    scan.seo.missingOgUrl
  );
}

function priorityIssues(scan: ScanResult) {
  const issues: Array<{ label: string; detail: string; tone: 'good' | 'warn' | 'bad' | 'muted' }> = [];
  const gaps = totalSEOGaps(scan);
  issues.push({
    label: gaps ? 'Metadata gaps found' : 'Metadata coverage looks good',
    detail: gaps ? `${gaps} missing SEO or Open Graph fields across analysed pages.` : 'No missing title, H1, description, canonical, or key OG fields found.',
    tone: gaps ? 'warn' : 'good',
  });
  issues.push({
    label: scan.summary.failedPages ? 'Some pages failed to fetch' : 'Crawl fetches are clean',
    detail: scan.summary.failedPages ? `${scan.summary.failedPages} pages failed during source analysis.` : 'No source-analysis failures have been recorded.',
    tone: scan.summary.failedPages ? 'bad' : 'good',
  });
  issues.push({
    label: scan.summary.auditFailedPages ? 'Lighthouse needs review' : 'Lighthouse is non-blocking',
    detail: scan.summary.auditFailedPages
      ? `${scan.summary.auditFailedPages} Lighthouse audits failed, but source data is still available.`
      : lighthouseLabel(scan.summary),
    tone: scan.summary.auditFailedPages ? 'warn' : 'muted',
  });
  issues.push({
    label: scan.links.external > scan.links.internal ? 'External links dominate' : 'Internal navigation dominates',
    detail: `${scan.links.internal} internal links and ${scan.links.external} external links were found.`,
    tone: scan.links.external > scan.links.internal ? 'warn' : 'muted',
  });
  return issues;
}

function pageIssues(page: PageResult) {
  const issues: Array<{ label: string; detail: string; tone: 'good' | 'warn' | 'bad' | 'muted' }> = [];
  if (page.fetchError) {
    issues.push({ label: 'Fetch failed', detail: page.fetchError, tone: 'bad' });
  }
  if (!page.title) {
    issues.push({ label: 'Missing title', detail: 'Add a page title for browser tabs and search results.', tone: 'warn' });
  }
  if (!page.h1) {
    issues.push({ label: 'Missing H1', detail: 'Add one primary heading so users and crawlers understand the page.', tone: 'warn' });
  }
  if (!page.description) {
    issues.push({ label: 'Missing description', detail: 'Add a meta description for search result summaries.', tone: 'warn' });
  }
  if (!page.canonical) {
    issues.push({ label: 'Missing canonical', detail: 'Add a canonical URL to reduce duplicate URL ambiguity.', tone: 'warn' });
  }
  if (!page.og.title || !page.og.image || !page.og.url) {
    issues.push({ label: 'Open Graph incomplete', detail: 'Add OG title, image, and URL for better sharing previews.', tone: 'warn' });
  }
  if (page.auditError) {
    issues.push({ label: 'Lighthouse failed', detail: page.auditError, tone: 'warn' });
  }
  return issues;
}

function pageIssueCount(page: PageResult) {
  return pageIssues(page).length;
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
