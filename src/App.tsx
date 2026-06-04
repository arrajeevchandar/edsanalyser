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
import {
  cancelComparison,
  cancelScan,
  checkEds,
  getComparison,
  getScan,
  listComparisons,
  listScans,
  reauditScan,
  startComparison,
  startScan,
} from './api';
import type {
  BlockStat,
  ComparedPage,
  ComparisonResult,
  ComparisonSummary,
  FieldDiff,
  PageResult,
  ScanEvent,
  ScanResult,
  ScanSummary,
  SectionStat,
  VisualDiff,
} from './types';

type Mode = 'compare' | 'scan';
type Tab = 'overview' | 'pages' | 'blocks' | 'links' | 'seo' | 'history';
type CompareTab = 'overview' | 'pages' | 'visual' | 'blocks' | 'links' | 'seo' | 'history';
type CompareGroupFilter = 'all' | 'matched' | 'missing' | 'extra' | 'source-failed' | 'eds-failed' | 'uncertain';
type ComparisonPageGroup = Exclude<CompareGroupFilter, 'all'>;

interface ComparisonPageRow {
  id: string;
  group: ComparisonPageGroup;
  path: string;
  status: string;
  severity: number;
  source?: PageResult;
  eds?: PageResult;
  fieldDiffs: FieldDiff[];
  linkDiffs: FieldDiff[];
  visuals: VisualDiff[];
  issues: string[];
  matchType: string;
  matchConfidence: string;
  sourceAliases: string[];
  edsAliases: string[];
}

const tabs: Array<{ id: Tab; label: string; icon: typeof Activity }> = [
  { id: 'overview', label: 'Overview', icon: Activity },
  { id: 'pages', label: 'Pages', icon: FileSearch },
  { id: 'blocks', label: 'Blocks', icon: Boxes },
  { id: 'links', label: 'Links', icon: Link2 },
  { id: 'seo', label: 'SEO / OG', icon: ShieldCheck },
  { id: 'history', label: 'History', icon: History },
];

const compareTabs: Array<{ id: CompareTab; label: string; icon: typeof Activity }> = [
  { id: 'overview', label: 'Overview', icon: Activity },
  { id: 'pages', label: 'Pages', icon: FileSearch },
  { id: 'visual', label: 'Visual', icon: PanelTop },
  { id: 'blocks', label: 'Blocks', icon: Boxes },
  { id: 'links', label: 'Links', icon: Link2 },
  { id: 'seo', label: 'SEO / OG', icon: ShieldCheck },
  { id: 'history', label: 'History', icon: History },
];

const compareGroupOptions: Array<{ id: CompareGroupFilter; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'matched', label: 'Matched' },
  { id: 'uncertain', label: 'Uncertain' },
  { id: 'missing', label: 'Missing' },
  { id: 'extra', label: 'Extra' },
  { id: 'source-failed', label: 'Source fail' },
  { id: 'eds-failed', label: 'EDS fail' },
];

export default function App() {
  const [mode, setMode] = useState<Mode>('compare');
  const [url, setUrl] = useState('');
  const [sourceUrl, setSourceUrl] = useState('');
  const [edsUrl, setEdsUrl] = useState('');
  const [scan, setScan] = useState<ScanResult | null>(null);
  const [comparison, setComparison] = useState<ComparisonResult | null>(null);
  const [history, setHistory] = useState<ScanSummary[]>([]);
  const [comparisonHistory, setComparisonHistory] = useState<ComparisonSummary[]>([]);
  const [activeScan, setActiveScan] = useState<ScanSummary | null>(null);
  const [activeComparison, setActiveComparison] = useState<ComparisonSummary | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [compareTab, setCompareTab] = useState<CompareTab>('overview');
  const [pageFilter, setPageFilter] = useState('');
  const [compareFilter, setCompareFilter] = useState('');
  const [compareGroupFilter, setCompareGroupFilter] = useState<CompareGroupFilter>('all');
  const [selectedPageURL, setSelectedPageURL] = useState<string | null>(null);
  const [selectedCompareID, setSelectedCompareID] = useState<string | null>(null);
  const [visualViewport, setVisualViewport] = useState<'desktop' | 'mobile'>('desktop');
  const [events, setEvents] = useState<ScanEvent[]>([]);
  const [comparisonEvents, setComparisonEvents] = useState<ScanEvent[]>([]);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [compareLoading, setCompareLoading] = useState(false);
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

  useEffect(() => {
    if (!activeComparison || activeComparison.status !== 'running') {
      return undefined;
    }
    const source = new EventSource(`/api/comparisons/${activeComparison.id}/events`);
    const eventNames = [
      'start',
      'source-discovered',
      'source-page-start',
      'source-page-analyzed',
      'eds-discovered',
      'eds-page-start',
      'eds-page-analyzed',
      'matching',
      'fast-complete',
      'comparison-audit-complete',
      'visual-complete',
      'cancel',
      'complete',
    ];
    const handleEvent = (message: MessageEvent) => {
      const parsed = JSON.parse(message.data) as ScanEvent;
      setComparisonEvents((current) => [parsed, ...current].slice(0, 10));
      if (['source-page-analyzed', 'eds-page-analyzed', 'matching', 'fast-complete', 'comparison-audit-complete', 'visual-complete', 'complete'].includes(parsed.type)) {
        void loadComparison(activeComparison.id);
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
  }, [activeComparison?.id, activeComparison?.status]);

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

  const comparisonRows = useMemo(() => buildComparisonRows(comparison), [comparison]);

  const selectedComparisonRow = useMemo(() => {
    if (!comparisonRows.length) {
      return null;
    }
    return comparisonRows.find((row) => row.id === selectedCompareID) || preferredComparisonRow(comparisonRows) || comparisonRows[0];
  }, [comparisonRows, selectedCompareID]);

  const filteredComparisonRows = useMemo(() => {
    const value = compareFilter.trim().toLowerCase();
    return comparisonRows.filter((row) => {
      if (compareGroupFilter !== 'all' && row.group !== compareGroupFilter) {
        return false;
      }
      if (!value) {
        return true;
      }
      return [
        row.path,
        row.status,
        row.group,
        row.matchType,
        row.source?.title,
        row.eds?.title,
        row.source?.url,
        row.eds?.url,
        ...row.issues,
      ]
        .filter((item): item is string => Boolean(item))
        .some((item) => item.toLowerCase().includes(value));
    });
  }, [comparisonRows, compareFilter, compareGroupFilter]);

  async function refreshHistory() {
    try {
      const [scanItems, comparisonItems] = await Promise.all([listScans(), listComparisons()]);
      setHistory(scanItems);
      setComparisonHistory(comparisonItems);
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

  async function loadComparison(id: string) {
    try {
      const result = await getComparison(id);
      setComparison(result);
      setActiveComparison(result.summary);
      const rows = buildComparisonRows(result);
      setSelectedCompareID((current) => current || preferredComparisonRow(rows)?.id || rows[0]?.id || null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to load comparison');
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

  async function onStartComparison(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    setComparisonEvents([]);
    setCompareLoading(true);
    try {
      const created = await startComparison(sourceUrl, edsUrl, null);
      setActiveComparison(created);
      setComparison({
        summary: created,
        discovery: emptyComparisonDiscovery,
        matched: [],
        uncertainMatches: [],
        missingInEDS: [],
        extraInEDS: [],
        sourceFetchFailures: [],
        edsFetchFailures: [],
        blocks: [],
        sections: [],
        links: emptyComparisonLinks,
        seo: emptyComparisonSEO,
        generatedAt: new Date().toISOString(),
      });
      setSelectedCompareID(null);
      setCompareGroupFilter('all');
      setCompareTab('overview');
      setMode('compare');
      await refreshHistory();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unable to start comparison');
    } finally {
      setCompareLoading(false);
    }
  }

  async function onCancelScan() {
    if (!activeScan) {
      return;
    }
    await cancelScan(activeScan.id).catch((err) => setError(err instanceof Error ? err.message : 'Unable to cancel scan'));
  }

  async function onCancelComparison() {
    if (!activeComparison) {
      return;
    }
    await cancelComparison(activeComparison.id).catch((err) => setError(err instanceof Error ? err.message : 'Unable to cancel comparison'));
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

  function onCompareGroupFilter(value: CompareGroupFilter) {
    setCompareGroupFilter(value);
    const next = value === 'all' ? preferredComparisonRow(comparisonRows) || comparisonRows[0] : comparisonRows.find((row) => row.group === value);
    setSelectedCompareID(next?.id || null);
  }

  const summary = scan?.summary || activeScan;
  const comparisonSummary = comparison?.summary || activeComparison;
  const isScanRunning = summary?.status === 'running';
  const isCompareRunning = comparisonSummary?.status === 'running';
  const isRunning = mode === 'scan' ? isScanRunning : isCompareRunning;

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

        <div className="mode-switch" role="group" aria-label="Dashboard mode">
          <button type="button" className={mode === 'compare' ? 'active' : ''} onClick={() => setMode('compare')}>Compare</button>
          <button type="button" className={mode === 'scan' ? 'active' : ''} onClick={() => setMode('scan')}>Scan</button>
        </div>

        {mode === 'scan' ? (
          <form className="scanbar" onSubmit={onStartScan}>
            <label htmlFor="site-url" className="sr-only">EDS URL</label>
            <div className="scanbar-field">
              <Search size={18} className="scanbar-icon" aria-hidden />
              <input
                id="site-url"
                value={url}
                onChange={(event) => setUrl(event.target.value)}
                placeholder="Enter an EDS site URL to analyse..."
                disabled={loading || isScanRunning}
              />
              <button type="submit" className="btn btn-primary" disabled={loading || isScanRunning || !url.trim()} title="Start scan">
                {loading ? <Loader2 className="spin" size={17} /> : <Play size={16} strokeWidth={2.6} />}
                <span className="btn-label">Analyse</span>
              </button>
            </div>
          </form>
        ) : (
          <form className="comparebar" onSubmit={onStartComparison}>
            <label htmlFor="source-url" className="sr-only">Legacy site URL</label>
            <label htmlFor="eds-url" className="sr-only">Migrated EDS URL</label>
            <div className="comparebar-fields">
              <div className="comparebar-field">
                <span>Legacy</span>
                <input
                  id="source-url"
                  value={sourceUrl}
                  onChange={(event) => setSourceUrl(event.target.value)}
                  placeholder="Non-EDS source URL"
                  disabled={compareLoading || isCompareRunning}
                />
              </div>
              <div className="comparebar-field">
                <span>EDS</span>
                <input
                  id="eds-url"
                  value={edsUrl}
                  onChange={(event) => setEdsUrl(event.target.value)}
                  placeholder="Migrated EDS URL"
                  disabled={compareLoading || isCompareRunning}
                />
              </div>
              <button type="submit" className="btn btn-primary" disabled={compareLoading || isCompareRunning || !sourceUrl.trim() || !edsUrl.trim()} title="Start comparison">
                {compareLoading ? <Loader2 className="spin" size={17} /> : <Play size={16} strokeWidth={2.6} />}
                <span className="btn-label">Compare</span>
              </button>
            </div>
          </form>
        )}

        <div className="appbar-status">
          {isRunning && (
            <button type="button" className="btn btn-ghost btn-danger" onClick={mode === 'scan' ? onCancelScan : onCancelComparison}>
              <StopCircle size={16} />
              <span className="btn-label">Cancel</span>
            </button>
          )}
          <div className={`status-pill ${mode === 'scan' ? summary?.status || 'idle' : comparisonSummary?.status || 'idle'}`}>
            <span className={`status-dot ${isRunning ? 'pulse' : ''}`} aria-hidden />
            {mode === 'scan' ? summary?.status || 'idle' : comparisonSummary?.status || 'idle'}
          </div>
        </div>
      </header>

      <nav className="tabrail" aria-label="Sections">
        {(mode === 'scan' ? tabs : compareTabs).map((item) => {
          const Icon = item.icon;
          return (
            <button
              key={item.id}
              type="button"
              className={mode === 'scan' ? tab === item.id ? 'active' : '' : compareTab === item.id ? 'active' : ''}
              onClick={() => (mode === 'scan' ? setTab(item.id as Tab) : setCompareTab(item.id as CompareTab))}
            >
              <Icon size={17} />
              {item.label}
            </button>
          );
        })}
      </nav>

      <main className="canvas">
        {mode === 'scan' ? (
          <>
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
          </>
        ) : (
          <CompareDashboard
            comparison={comparison}
            summary={comparisonSummary}
            history={comparisonHistory}
            scanHistory={history}
            tab={compareTab}
            events={comparisonEvents}
            error={error}
            selectedRow={selectedComparisonRow}
            rows={filteredComparisonRows}
            pageFilter={compareFilter}
            groupFilter={compareGroupFilter}
            visualViewport={visualViewport}
            onFilter={setCompareFilter}
            onGroupFilter={onCompareGroupFilter}
            onSelect={setSelectedCompareID}
            onOpen={(id) => void loadComparison(id)}
            onViewport={setVisualViewport}
          />
        )}
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

function CompareDashboard({
  comparison,
  summary,
  history,
  scanHistory,
  tab,
  events,
  error,
  selectedRow,
  rows,
  pageFilter,
  groupFilter,
  visualViewport,
  onFilter,
  onGroupFilter,
  onSelect,
  onOpen,
  onViewport,
}: {
  comparison: ComparisonResult | null;
  summary: ComparisonSummary | null;
  history: ComparisonSummary[];
  scanHistory: ScanSummary[];
  tab: CompareTab;
  events: ScanEvent[];
  error: string;
  selectedRow: ComparisonPageRow | null;
  rows: ComparisonPageRow[];
  pageFilter: string;
  groupFilter: CompareGroupFilter;
  visualViewport: 'desktop' | 'mobile';
  onFilter: (value: string) => void;
  onGroupFilter: (value: CompareGroupFilter) => void;
  onSelect: (value: string) => void;
  onOpen: (id: string) => void;
  onViewport: (value: 'desktop' | 'mobile') => void;
}) {
  return (
    <>
      <section className="hero">
        <div className="hero-title">
          <p className="eyebrow">Migration comparison</p>
          <h1>{summary ? `${readableHost(summary.sourceRootUrl)} to ${readableHost(summary.edsRootUrl)}` : 'Compare legacy to EDS'}</h1>
          <p className="hero-copy">
            {summary
              ? `${comparisonPhaseLabel(summary)} - path-matched migration checks, visual diffs, SEO parity, links, blocks, and Lighthouse deltas.`
              : 'Enter a legacy site URL and its migrated EDS URL to find missing pages, metadata drift, link changes, block issues, and visual regressions.'}
          </p>
          {summary && <span className="hero-phase">{comparisonPhaseLabel(summary)}</span>}
        </div>
        {summary && (
          <div className="hero-health">
            <ScoreBadge score={summary.migrationScore} />
            <span>Migration score</span>
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
          <Metric label="Phase" value={comparisonPhaseLabel(summary)} />
          <Metric label="Matched" value={summary.matchedPages.toString()} />
          <Metric label="Missing" value={summary.missingInEDS.toString()} tone={summary.missingInEDS ? 'bad' : 'good'} />
          <Metric label="Visual" value={`${summary.visualCompleted}/${summary.visualQueued}`} />
          <Metric label="Lighthouse" value={`${summary.lighthouseCompleted}/${summary.lighthouseQueued}`} />
        </section>
      )}

      {events.length > 0 && (
        <section className="event-strip" aria-label="Live comparison activity">
          {events.map((event) => (
            <span key={`${event.timestamp}-${event.type}-${event.pageUrl || ''}`}>
              <span className="event-dot" aria-hidden />
              {event.type.replace(/-/g, ' ')}
              {event.pageUrl ? `: ${compactURL(event.pageUrl)}` : event.message ? `: ${event.message}` : ''}
            </span>
          ))}
        </section>
      )}

      {!comparison && <CompareEmptyState history={history} onOpen={onOpen} />}

      {comparison && tab === 'overview' && <ComparisonOverview comparison={comparison} />}
      {comparison && tab === 'pages' && (
        <ComparisonPagesView
          comparison={comparison}
          rows={rows}
          selectedRow={selectedRow}
          pageFilter={pageFilter}
          groupFilter={groupFilter}
          onFilter={onFilter}
          onGroupFilter={onGroupFilter}
          onSelect={onSelect}
          visualViewport={visualViewport}
          onViewport={onViewport}
        />
      )}
      {comparison && tab === 'visual' && <ComparisonVisualView pages={[...comparison.matched, ...comparison.uncertainMatches]} viewport={visualViewport} onViewport={onViewport} />}
      {comparison && tab === 'blocks' && <ComparisonBlocksView comparison={comparison} />}
      {comparison && tab === 'links' && <ComparisonLinksView comparison={comparison} />}
      {comparison && tab === 'seo' && <ComparisonSEOView comparison={comparison} />}
      {tab === 'history' && <ComparisonHistoryView history={history} scanHistory={scanHistory} currentID={summary?.id} onOpen={onOpen} />}
    </>
  );
}

function CompareEmptyState({ history, onOpen }: { history: ComparisonSummary[]; onOpen: (id: string) => void }) {
  return (
    <section className="empty-state">
      <div className="empty-hero">
        <span className="empty-icon">
          <Route size={30} strokeWidth={1.8} />
        </span>
        <h2>No comparison selected</h2>
        <p>
          Add a legacy URL and a migrated EDS URL in the bar above. The crawler will match paths,
          compare content signals, and run visual checks for desktop and mobile.
        </p>
      </div>
      {history.length > 0 && (
        <div className="empty-recent">
          <p className="eyebrow">Recent comparisons</p>
          <div className="recent-grid">
            {history.slice(0, 4).map((item) => (
              <button key={item.id} type="button" className="recent-card" onClick={() => onOpen(item.id)}>
                <span className="recent-host">{readableHost(item.sourceRootUrl)} to {readableHost(item.edsRootUrl)}</span>
                <span className="recent-meta">{item.matchedPages} matched / {item.missingInEDS} missing</span>
                <ScoreBadge score={item.migrationScore} />
              </button>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function ComparisonOverview({ comparison }: { comparison: ComparisonResult }) {
  const summary = comparison.summary;
  const pairedPages = [...comparison.matched, ...comparison.uncertainMatches];
  const issuePages = pairedPages.filter((page) => page.status !== 'pass').length;
  const zeroBlockPages = pairedPages.filter((page) => page.eds.blockCount === 0).length;
  const cards = [
    { label: 'Matched paths', value: summary.matchedPages.toString(), detail: `${summary.uncertainMatches} uncertain aliases`, tone: 'good' as const, icon: <Route size={18} /> },
    { label: 'Missing in EDS', value: summary.missingInEDS.toString(), detail: 'Legacy pages without migrated match', tone: summary.missingInEDS ? 'bad' as const : 'good' as const, icon: <OctagonX size={18} /> },
    { label: 'Page issues', value: issuePages.toString(), detail: 'Matched pages needing review', tone: issuePages ? 'warn' as const : 'good' as const, icon: <AlertCircle size={18} /> },
    { label: 'Visual failures', value: summary.visualFail.toString(), detail: `${summary.visualReview} visual reviews`, tone: summary.visualFail ? 'bad' as const : summary.visualReview ? 'warn' as const : 'good' as const, icon: <PanelTop size={18} /> },
  ];
  return (
    <section className="overview-stack">
      <section className="insight-hero-panel">
        <div className="insight-narrative">
          <p className="eyebrow">Migration readout</p>
          <h2>{comparisonReadout(comparison)}</h2>
          <p>Start with missing pages and visual failures, then drill into page-level metadata, link, block, and screenshot differences.</p>
        </div>
        <div className="insight-card-grid">
          {cards.map((item) => <InsightCard key={item.label} {...item} />)}
        </div>
      </section>
      <section className="overview-grid">
        <div className="panel priority-panel">
          <div className="panel-heading"><h2>Priority checks</h2><ClipboardList size={19} /></div>
          <div className="issue-stack">
            <IssueItem label="Missing pages" detail={`${summary.missingInEDS} source paths are not present in EDS.`} tone={summary.missingInEDS ? 'bad' : 'good'} />
            <IssueItem label="Metadata drift" detail={`${summary.metadataDiffs} title, H1, SEO, canonical, or OG differences.`} tone={summary.metadataDiffs ? 'warn' : 'good'} />
            <IssueItem label="Link drift" detail={`${summary.linkDiffs} matched page link differences.`} tone={summary.linkDiffs ? 'warn' : 'good'} />
            <IssueItem label="EDS block coverage" detail={`${zeroBlockPages} matched EDS pages have no detected blocks.`} tone={zeroBlockPages ? 'warn' : 'good'} />
          </div>
        </div>
        <div className="panel">
          <div className="panel-heading"><h2>Coverage</h2><FileSearch size={19} /></div>
          <div className="mini-grid">
            <Metric label="Source pages" value={summary.sourcePages.toString()} />
            <Metric label="EDS pages" value={summary.edsPages.toString()} />
            <Metric label="Uncertain" value={summary.uncertainMatches.toString()} tone={summary.uncertainMatches ? 'warn' : 'good'} />
            <Metric label="Extra EDS" value={summary.extraInEDS.toString()} tone={summary.extraInEDS ? 'warn' : 'good'} />
            <Metric label="Fetch failures" value={(summary.sourceFetchFailures + summary.edsFetchFailures).toString()} tone={summary.sourceFetchFailures + summary.edsFetchFailures ? 'bad' : 'good'} />
          </div>
        </div>
        <DiscoveryPanel title="Legacy discovery" report={comparison.discovery.source} />
        <DiscoveryPanel title="EDS discovery" report={comparison.discovery.eds} />
        <div className="panel">
          <div className="panel-heading"><h2>Visual checks</h2><PanelTop size={19} /></div>
          <div className="mini-grid">
            <Metric label="Queued" value={summary.visualQueued.toString()} />
            <Metric label="Complete" value={summary.visualCompleted.toString()} />
            <Metric label="Review" value={summary.visualReview.toString()} tone={summary.visualReview ? 'warn' : 'good'} />
            <Metric label="Fail" value={summary.visualFail.toString()} tone={summary.visualFail ? 'bad' : 'good'} />
          </div>
        </div>
        <div className="panel">
          <div className="panel-heading"><h2>Lighthouse</h2><BarChart3 size={19} /></div>
          <div className="mini-grid">
            <Metric label="Queued" value={summary.lighthouseQueued.toString()} />
            <Metric label="Complete" value={summary.lighthouseCompleted.toString()} />
            <Metric label="Failed" value={summary.lighthouseFailed.toString()} tone={summary.lighthouseFailed ? 'warn' : 'good'} />
          </div>
        </div>
      </section>
    </section>
  );
}

function DiscoveryPanel({ title, report }: { title: string; report: ComparisonResult['discovery']['source'] }) {
  const warning = report.limitHit || report.totalAnalyzed <= 1 || report.warnings.length > 0;
  return (
    <div className="panel discovery-panel">
      <div className="panel-heading"><h2>{title}</h2><FileSearch size={19} /></div>
      <div className="mini-grid">
        <Metric label="Queued" value={report.totalQueued.toString()} />
        <Metric label="Analyzed" value={report.totalAnalyzed.toString()} tone={report.totalAnalyzed <= 1 ? 'warn' : 'good'} />
        <Metric label="Sitemaps" value={(report.fromSitemap + report.fromRobots).toString()} />
        <Metric label="Query index" value={report.fromQueryIndex.toString()} />
        <Metric label="Static links" value={report.fromStaticLinks.toString()} />
        <Metric label="Rendered" value={report.fromRenderedLinks.toString()} tone={report.fromRenderedLinks ? 'warn' : 'muted'} />
      </div>
      {warning && (
        <div className="issue-stack tight">
          {report.limitHit && <IssueItem label="Crawl limit hit" detail="The crawler stopped before queuing every discovered URL." tone="warn" />}
          {report.totalAnalyzed <= 1 && <IssueItem label="Only one page discovered" detail="Sitemap, query-index, static links, and rendered links exposed no additional pages." tone="warn" />}
          {report.warnings.slice(0, 3).map((item) => <IssueItem key={item} label="Discovery note" detail={item} tone="muted" />)}
        </div>
      )}
    </div>
  );
}

function ComparisonPagesView({
  comparison,
  rows,
  selectedRow,
  pageFilter,
  groupFilter,
  onFilter,
  onGroupFilter,
  onSelect,
  visualViewport,
  onViewport,
}: {
  comparison: ComparisonResult;
  rows: ComparisonPageRow[];
  selectedRow: ComparisonPageRow | null;
  pageFilter: string;
  groupFilter: CompareGroupFilter;
  onFilter: (value: string) => void;
  onGroupFilter: (value: CompareGroupFilter) => void;
  onSelect: (value: string) => void;
  visualViewport: 'desktop' | 'mobile';
  onViewport: (value: 'desktop' | 'mobile') => void;
}) {
  const matchedPairs = [...comparison.matched, ...comparison.uncertainMatches];
  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div>
          <p className="eyebrow">Page coverage</p>
          <h2>Review every matched and unmatched path</h2>
        </div>
        <div className="search-field">
          <Search size={16} />
          <input value={pageFilter} onChange={(event) => onFilter(event.target.value)} placeholder="Filter compared pages" />
        </div>
      </div>
      <div className="segmented-control compare-filter-control">
        {compareGroupOptions.map((item) => (
          <button key={item.id} type="button" className={groupFilter === item.id ? 'active' : ''} onClick={() => onGroupFilter(item.id)}>
            {item.label}
          </button>
        ))}
      </div>
      <div className="summary-ribbon">
        <Metric label="Matched" value={comparison.summary.matchedPages.toString()} />
        <Metric label="Uncertain" value={comparison.summary.uncertainMatches.toString()} tone={comparison.summary.uncertainMatches ? 'warn' : 'good'} />
        <Metric label="Missing" value={comparison.summary.missingInEDS.toString()} tone={comparison.summary.missingInEDS ? 'bad' : 'good'} />
        <Metric label="Extra" value={comparison.summary.extraInEDS.toString()} tone={comparison.summary.extraInEDS ? 'warn' : 'good'} />
        <Metric label="Reviews" value={matchedPairs.filter((page) => page.status === 'review').length.toString()} />
        <Metric label="Fails" value={matchedPairs.filter((page) => page.status === 'fail').length.toString()} tone={matchedPairs.some((page) => page.status === 'fail') ? 'bad' : 'good'} />
      </div>
      <section className="pages-layout">
        <div className="panel table-panel">
          <div className="panel-heading"><h2>Compared paths</h2><span className="panel-count">{rows.length} shown</span></div>
          <div className="table-scroll page-table-scroll">
            <table>
              <thead><tr><th>Path</th><th>Group</th><th>Status</th><th>Match</th><th>Issues</th></tr></thead>
              <tbody>
                {rows.map((row) => (
                  <tr key={row.id} onClick={() => onSelect(row.id)} className={selectedRow?.id === row.id ? 'selected' : ''}>
                    <td><span className="url-cell">{row.path}</span></td>
                    <td><span className={`link-kind ${row.group}`}>{groupLabel(row.group)}</span></td>
                    <td><span className={`audit-status ${row.status}`}>{row.status}</span></td>
                    <td>{row.matchType} / {row.matchConfidence}</td>
                    <td>{row.fieldDiffs.length + row.linkDiffs.length + row.issues.length}</td>
                  </tr>
                ))}
                {rows.length === 0 && <EmptyTableRow columns={5} message="No pages in this group yet" />}
              </tbody>
            </table>
          </div>
        </div>
        <ComparedPageDetail row={selectedRow} viewport={visualViewport} onViewport={onViewport} />
      </section>
    </section>
  );
}

function ComparedPageDetail({ row, viewport, onViewport }: { row: ComparisonPageRow | null; viewport: 'desktop' | 'mobile'; onViewport: (value: 'desktop' | 'mobile') => void }) {
  if (!row) {
    return <div className="panel detail-panel page-inspector"><h2>Page detail</h2><EmptyInline message="Select a page to inspect migration differences." /></div>;
  }
  const visual = row.visuals.find((item) => item.viewport === viewport);
  const canCompare = Boolean(row.source && row.eds);
  return (
    <div className="panel detail-panel page-inspector compare-inspector">
      <div className="inspector-head">
        <div>
          <p className="eyebrow">{groupLabel(row.group)}</p>
          <h2>{row.path}</h2>
          <span>{row.matchType} match / {row.matchConfidence} confidence</span>
        </div>
        <span className={`audit-status ${row.status}`}>{row.status}</span>
      </div>
      <div className="compare-columns">
        {row.source ? <SideBySideMeta title="Legacy source" page={row.source} /> : <MissingSide title="Legacy source" message="Not found in source crawl." />}
        {row.eds ? <SideBySideMeta title="Migrated EDS" page={row.eds} /> : <MissingSide title="Migrated EDS" message="EDS page missing." />}
      </div>
      <div className="issue-stack tight">
        {[...row.issues, ...row.fieldDiffs.map((diff) => `${diff.field}: ${diff.source || 'missing'} -> ${diff.eds || 'missing'}`), ...row.linkDiffs.map((diff) => diff.field)].slice(0, 8).map((issue) => (
          <IssueItem key={issue} label={issue} detail="Review this page before launch." tone="warn" />
        ))}
        {!canCompare && <IssueItem label={row.group === 'missing' ? 'EDS page missing' : row.group === 'extra' ? 'Extra EDS page' : 'Fetch failed'} detail="This row cannot be compared side-by-side until both pages fetch successfully." tone={row.group === 'extra' ? 'warn' : 'bad'} />}
        {row.issues.length === 0 && row.fieldDiffs.length === 0 && row.linkDiffs.length === 0 && canCompare && <IssueItem label="Page parity looks good" detail="No source/EDS metadata or link differences detected." tone="good" />}
      </div>
      {canCompare && (
        <>
          <VisualToggle viewport={viewport} onViewport={onViewport} />
          <VisualPreview visual={visual} />
        </>
      )}
    </div>
  );
}

function MissingSide({ title, message }: { title: string; message: string }) {
  return (
    <div className="compare-meta-panel missing-side">
      <h3>{title}</h3>
      <EmptyInline message={message} />
    </div>
  );
}

function SideBySideMeta({ title, page }: { title: string; page: PageResult }) {
  return (
    <div className="compare-meta-panel">
      <h3>{title}</h3>
      <MetaItem label="Title" value={page.title} />
      <MetaItem label="H1" value={page.h1} />
      <MetaItem label="Description" value={page.description} />
      <MetaItem label="Canonical" value={page.canonical} />
      <MetaItem label="OG image" value={page.og.image} />
      <Metric label="Links" value={(page.linkCount || 0).toString()} />
      <Metric label="Blocks" value={(page.blockCount || 0).toString()} />
    </div>
  );
}

function ComparisonVisualView({ pages, viewport, onViewport }: { pages: ComparedPage[]; viewport: 'desktop' | 'mobile'; onViewport: (value: 'desktop' | 'mobile') => void }) {
  const visuals = pages.flatMap((page) => page.visuals.filter((visual) => visual.viewport === viewport).map((visual) => ({ page, visual })));
  return (
    <section className="page-workspace">
      <div className="section-toolbar">
        <div><p className="eyebrow">Visual diffs</p><h2>First viewport screenshot comparison</h2></div>
        <VisualToggle viewport={viewport} onViewport={onViewport} />
      </div>
      <div className="visual-grid">
        {visuals.map(({ page, visual }) => (
          <div key={`${page.path}-${visual.viewport}`} className={`visual-card ${visual.status}`}>
            <div className="visual-card-head">
              <strong>{page.path}</strong>
              <span>{visual.status} / {visual.diffPercent}%</span>
            </div>
            <VisualPreview visual={visual} />
          </div>
        ))}
        {visuals.length === 0 && <EmptyInline message="Visual diffs have not completed yet." />}
      </div>
    </section>
  );
}

function VisualToggle({ viewport, onViewport }: { viewport: 'desktop' | 'mobile'; onViewport: (value: 'desktop' | 'mobile') => void }) {
  return (
    <div className="segmented-control">
      <button type="button" className={viewport === 'desktop' ? 'active' : ''} onClick={() => onViewport('desktop')}>Desktop</button>
      <button type="button" className={viewport === 'mobile' ? 'active' : ''} onClick={() => onViewport('mobile')}>Mobile</button>
    </div>
  );
}

function VisualPreview({ visual }: { visual?: VisualDiff }) {
  if (!visual) {
    return <EmptyInline message="Visual diff pending" />;
  }
  if (visual.error) {
    return <div className="warning-box">{visual.error}</div>;
  }
  return (
    <div className="visual-preview">
      {visual.sourceImage && <img src={visual.sourceImage} alt={`${visual.viewport} source screenshot`} />}
      {visual.edsImage && <img src={visual.edsImage} alt={`${visual.viewport} EDS screenshot`} />}
      {visual.diffImage && <img src={visual.diffImage} alt={`${visual.viewport} visual diff`} />}
    </div>
  );
}

function ComparisonBlocksView({ comparison }: { comparison: ComparisonResult }) {
  const zeroBlockPages = [...comparison.matched, ...comparison.uncertainMatches].filter((page) => page.eds.blockCount === 0);
  return (
    <section className="page-workspace">
      <div className="summary-ribbon">
        <Metric label="EDS block types" value={comparison.blocks.length.toString()} />
        <Metric label="Section variations" value={comparison.sections.length.toString()} />
        <Metric label="Zero-block pages" value={zeroBlockPages.length.toString()} tone={zeroBlockPages.length ? 'warn' : 'good'} />
      </div>
      {zeroBlockPages.length > 0 && (
        <div className="panel">
          <div className="panel-heading"><h2>Pages with no EDS blocks</h2><AlertCircle size={19} /></div>
          <div className="compact-list">
            {zeroBlockPages.slice(0, 12).map((page) => <div key={page.path} className="page-signal"><span>{page.path}</span><strong>0 blocks</strong></div>)}
          </div>
        </div>
      )}
      <BlocksView blocks={comparison.blocks} sections={comparison.sections} />
    </section>
  );
}

function ComparisonLinksView({ comparison }: { comparison: ComparisonResult }) {
  const diffs = [...comparison.matched, ...comparison.uncertainMatches].flatMap((page) => page.linkDiffs.map((diff) => ({ page: page.path, diff })));
  return (
    <section className="page-workspace">
      <div className="summary-ribbon">
        <Metric label="Source links" value={comparison.links.sourceTotal.toString()} />
        <Metric label="EDS links" value={comparison.links.edsTotal.toString()} />
        <Metric label="Missing internal" value={comparison.links.missingInternal.toString()} tone={comparison.links.missingInternal ? 'warn' : 'good'} />
        <Metric label="Added internal" value={comparison.links.addedInternal.toString()} />
        <Metric label="Asset diffs" value={(comparison.links.missingAssets + comparison.links.addedAssets).toString()} tone={comparison.links.missingAssets + comparison.links.addedAssets ? 'warn' : 'good'} />
      </div>
      <DiffTable title="Link differences" rows={diffs.map(({ page, diff }) => ({ page, field: diff.field, source: diff.source, eds: diff.eds }))} />
    </section>
  );
}

function ComparisonSEOView({ comparison }: { comparison: ComparisonResult }) {
  const rows = [...comparison.matched, ...comparison.uncertainMatches].flatMap((page) => page.fieldDiffs.map((diff) => ({ page: page.path, field: diff.field, source: diff.source, eds: diff.eds })));
  return (
    <section className="page-workspace">
      <div className="summary-ribbon">
        <Metric label="Metadata diffs" value={comparison.seo.metadataDiffs.toString()} tone={comparison.seo.metadataDiffs ? 'warn' : 'good'} />
        <Metric label="Title diffs" value={comparison.seo.titleDiffs.toString()} />
        <Metric label="H1 diffs" value={comparison.seo.h1Diffs.toString()} />
        <Metric label="Description diffs" value={comparison.seo.descriptionDiffs.toString()} />
        <Metric label="OG diffs" value={comparison.seo.ogDiffs.toString()} />
      </div>
      <DiffTable title="SEO / OG differences" rows={rows} />
    </section>
  );
}

function DiffTable({ title, rows }: { title: string; rows: Array<{ page: string; field: string; source: string; eds: string }> }) {
  return (
    <div className="panel table-panel">
      <div className="panel-heading"><h2>{title}</h2><span className="panel-count">{rows.length}</span></div>
      <div className="table-scroll no-x">
        <table className="fit-table compare-diff-table">
          <thead><tr><th>Page</th><th>Field</th><th>Source</th><th>EDS</th></tr></thead>
          <tbody>
            {rows.map((row, index) => (
              <tr key={`${row.page}-${row.field}-${index}`}>
                <td>{row.page}</td>
                <td>{row.field}</td>
                <td>{row.source || '-'}</td>
                <td>{row.eds || '-'}</td>
              </tr>
            ))}
            {rows.length === 0 && <EmptyTableRow columns={4} message="No differences found" />}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ComparisonHistoryView({ history, scanHistory, currentID, onOpen }: { history: ComparisonSummary[]; scanHistory: ScanSummary[]; currentID?: string; onOpen: (id: string) => void }) {
  return (
    <section className="page-workspace">
      <div className="summary-ribbon">
        <Metric label="Comparisons" value={history.length.toString()} />
        <Metric label="Scans" value={scanHistory.length.toString()} />
        <Metric label="Running" value={history.filter((item) => item.status === 'running').length.toString()} />
      </div>
      <div className="panel table-panel">
        <div className="panel-heading"><h2>Comparison history</h2><History size={19} /></div>
        <div className="table-scroll">
          <table>
            <thead><tr><th>Type</th><th>Source</th><th>EDS</th><th>Matched</th><th>Score</th><th>Started</th></tr></thead>
            <tbody>
              {history.map((item) => (
                <tr key={item.id} onClick={() => onOpen(item.id)} className={item.id === currentID ? 'selected' : ''}>
                  <td>Compare</td>
                  <td>{readableHost(item.sourceRootUrl)}</td>
                  <td>{readableHost(item.edsRootUrl)}</td>
                  <td>{item.matchedPages}</td>
                  <td><ScoreBadge score={item.migrationScore} /></td>
                  <td>{new Date(item.startedAt).toLocaleString()}</td>
                </tr>
              ))}
              {history.length === 0 && <EmptyTableRow columns={6} message="No comparisons yet" />}
            </tbody>
          </table>
        </div>
      </div>
    </section>
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

      <section className="links-stack">
        <div className="panel links-distribution-panel">
          <div className="panel-heading"><h2>Distribution</h2><Link2 size={19} /></div>
          <div className="links-distribution-body">
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
                detail={`${scan.links.uniqueInternal} internal, ${scan.links.uniqueExternal} external, and ${scan.links.uniqueAsset} media unique URLs across all pages.`}
                tone="muted"
              />
              <IssueItem
                label="Unlabeled anchors"
                detail={`${unlabeledLinks.length} links have no visible anchor text.`}
                tone={unlabeledLinks.length ? 'warn' : 'good'}
              />
            </div>
          </div>
        </div>
        <div className="panel wide table-panel">
          <div className="panel-heading">
            <h2>All links</h2>
            <span className="panel-count">Showing first 250</span>
          </div>
          <div className="table-scroll no-x">
            <table className="fit-table links-table">
              <thead><tr><th>Type</th><th>Text</th><th>URL</th><th>Page</th></tr></thead>
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

      <section className="split-workspace seo-workspace">
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

function buildComparisonRows(comparison: ComparisonResult | null): ComparisonPageRow[] {
  if (!comparison) {
    return [];
  }
  const rows: ComparisonPageRow[] = [];
  const addCompared = (group: ComparisonPageGroup, page: ComparedPage) => {
    rows.push({
      id: `${group}:${page.path}`,
      group,
      path: page.path,
      status: page.status || 'pass',
      severity: page.severity || 0,
      source: page.source,
      eds: page.eds,
      fieldDiffs: page.fieldDiffs || [],
      linkDiffs: page.linkDiffs || [],
      visuals: page.visuals || [],
      issues: page.issues || [],
      matchType: page.matchType || 'exact',
      matchConfidence: page.matchConfidence || 'high',
      sourceAliases: page.sourceAliases || [],
      edsAliases: page.edsAliases || [],
    });
  };
  comparison.matched.forEach((page) => addCompared('matched', page));
  comparison.uncertainMatches.forEach((page) => addCompared('uncertain', page));
  comparison.missingInEDS.forEach((page) => rows.push(pageOnlyRow('missing', page, undefined, 'fail', ['Missing in migrated EDS site'])));
  comparison.extraInEDS.forEach((page) => rows.push(pageOnlyRow('extra', undefined, page, 'review', ['Extra page in migrated EDS site'])));
  comparison.sourceFetchFailures.forEach((page) => rows.push(pageOnlyRow('source-failed', page, undefined, 'fail', [page.fetchError || 'Source page failed to fetch'])));
  comparison.edsFetchFailures.forEach((page) => rows.push(pageOnlyRow('eds-failed', undefined, page, 'fail', [page.fetchError || 'EDS page failed to fetch'])));
  return rows.sort((a, b) => {
    if (a.group !== b.group) {
      return groupSort(a.group) - groupSort(b.group);
    }
    if (a.severity !== b.severity) {
      return b.severity - a.severity;
    }
    return a.path.localeCompare(b.path);
  });
}

function pageOnlyRow(group: ComparisonPageGroup, source: PageResult | undefined, eds: PageResult | undefined, status: string, issues: string[]): ComparisonPageRow {
  const page = source || eds;
  const path = page ? comparisonPathFromURL(page.url) : '/';
  return {
    id: `${group}:${path}`,
    group,
    path,
    status,
    severity: status === 'fail' ? 10 : 4,
    source,
    eds,
    fieldDiffs: [],
    linkDiffs: [],
    visuals: [],
    issues,
    matchType: 'unmatched',
    matchConfidence: 'low',
    sourceAliases: [],
    edsAliases: [],
  };
}

function preferredComparisonRow(rows: ComparisonPageRow[]) {
  return rows.find((row) => row.group === 'matched') || rows.find((row) => row.group === 'uncertain');
}

function comparisonPathFromURL(raw: string) {
  try {
    const parsed = new URL(raw);
    let path = parsed.pathname || '/';
    path = path.replace(/\/+$/, '') || '/';
    const lower = path.toLowerCase();
    if (lower === '/index' || lower === '/index.html') {
      return '/';
    }
    return lower.replace(/\/index(\.html)?$/, '') || '/';
  } catch {
    return raw || '/';
  }
}

function groupSort(group: ComparisonPageGroup) {
  return {
    missing: 0,
    'source-failed': 1,
    'eds-failed': 2,
    uncertain: 3,
    matched: 4,
    extra: 5,
  }[group];
}

function groupLabel(group: ComparisonPageGroup) {
  return {
    matched: 'Matched',
    uncertain: 'Uncertain',
    missing: 'Missing in EDS',
    extra: 'Extra EDS',
    'source-failed': 'Source failed',
    'eds-failed': 'EDS failed',
  }[group];
}

function comparisonReadout(comparison: ComparisonResult) {
  const summary = comparison.summary;
  if (summary.status === 'running') {
    return 'Comparison is building: source crawl, EDS crawl, Lighthouse, and visual diffs are running in stages.';
  }
  if (summary.missingInEDS > 0) {
    return `${summary.missingInEDS} legacy pages are missing from the migrated EDS site.`;
  }
  if (summary.uncertainMatches > 0) {
    return `${summary.uncertainMatches} pages matched through canonical or OG aliases and need confirmation.`;
  }
  if (summary.visualFail > 0) {
    return `${summary.visualFail} visual comparisons failed and should be reviewed first.`;
  }
  if (summary.metadataDiffs > 0 || summary.linkDiffs > 0) {
    return `${summary.metadataDiffs + summary.linkDiffs} content, metadata, or link differences need review.`;
  }
  return 'Matched pages look aligned across coverage, metadata, links, and visual checks.';
}

function comparisonPhaseLabel(summary: ComparisonSummary) {
  switch (summary.phase || summary.status) {
    case 'source-crawl':
      return 'Crawling source';
    case 'eds-crawl':
      return 'Crawling EDS';
    case 'matching':
      return 'Matching pages';
    case 'fast-complete':
      return 'Fast comparison ready';
    case 'lighthouse':
      return 'Running Lighthouse';
    case 'visual-diff':
      return 'Running visual diff';
    case 'completed':
      return 'Complete';
    case 'cancelled':
      return 'Cancelled';
    default:
      return summary.status || 'Idle';
  }
}

function visualSummary(visuals: VisualDiff[]) {
  if (!visuals.length) {
    return 'pending';
  }
  const failed = visuals.filter((visual) => visual.status === 'fail' || visual.status === 'failed').length;
  const review = visuals.filter((visual) => visual.status === 'review').length;
  if (failed) {
    return `${failed} fail`;
  }
  if (review) {
    return `${review} review`;
  }
  return 'pass';
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

const emptyLinks = { total: 0, internal: 0, external: 0, asset: 0, mail: 0, tel: 0, hash: 0, uniqueInternal: 0, uniqueExternal: 0, uniqueAsset: 0 };
const emptySEO = { missingTitle: 0, missingDescription: 0, missingH1: 0, missingCanonical: 0, missingOgTitle: 0, missingOgImage: 0, missingOgUrl: 0 };
const emptyComparisonLinks = {
  sourceTotal: 0,
  edsTotal: 0,
  missingInternal: 0,
  addedInternal: 0,
  missingExternal: 0,
  addedExternal: 0,
  missingAssets: 0,
  addedAssets: 0,
  matchedPageDiffs: 0,
};
const emptyComparisonSEO = { metadataDiffs: 0, titleDiffs: 0, h1Diffs: 0, descriptionDiffs: 0, ogDiffs: 0 };
const emptyDiscoveryReport = {
  rootUrl: '',
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
};
const emptyComparisonDiscovery = { source: emptyDiscoveryReport, eds: emptyDiscoveryReport };
