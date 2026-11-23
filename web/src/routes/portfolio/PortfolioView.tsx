// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import { LayoutGrid, Rows3 } from 'lucide-react';
import { AnimatePresence } from 'motion/react';
import { useMemo, useState, type ReactNode } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import { usePortfolioHistory, type Portfolio } from '../../api/portfolio';
import { FilterChips } from '../../components/FilterChips';
import { Select, SearchInput } from '../../components/inputs';
import { PageHeader } from '../../components/layout';
import { Segmented } from '../../components/Segmented';
import { EmptyState } from '../../components/states';
import { formatInt, plural } from '../../format';
import { HeatStrip, type HeatRow } from '../../viz';
import { useFirstView } from '../firstView';
import { secondaryButtonClass } from '../../ui';
import {
  effectiveView,
  FILTERS,
  filterCounts,
  gateOf,
  historyFor,
  indexHistory,
  readParams,
  rememberedView,
  rememberView,
  SORTS,
  STALE_AFTER_DAYS,
  visibleItems,
  writeParams,
  type FilterKey,
  type PortfolioParams,
  type SortKey,
  type ViewKey,
} from './model';
import { ServiceCard } from './ServiceCard';
import { SetupSteps } from './SetupSteps';
import { SummaryBand } from './SummaryBand';

/** Cards drawn at first; the rest wait for "Show all", so 500 services stay quick. */
const FIRST_CARDS = 48;

/**
 * The portfolio once loaded: a summary band, the controls, and the services
 * as cards or as a ranked list. `now` fixes the clock for tests.
 */
export function PortfolioView({
  data,
  now,
  notice,
}: {
  data: Portfolio;
  now?: Date;
  /** Something an administrator must know first, under the header. */
  notice?: ReactNode;
}) {
  const [search, setSearch] = useSearchParams();
  const navigate = useNavigate();
  const [mounted] = useState(() => new Date());
  const clock = now ?? mounted;
  const params = readParams(search);
  const [stored, setStored] = useState(rememberedView);
  const view = effectiveView(params.view, stored, data.items.length);
  const [showAll, setShowAll] = useState(false);
  const first = useFirstView('portfolio');

  const history = usePortfolioHistory(data.items.length > 0);
  const index = useMemo(() => indexHistory(history.data), [history.data]);

  function update(patch: Partial<PortfolioParams>) {
    setSearch(writeParams({ ...params, ...patch }), { replace: true });
  }

  function chooseView(next: ViewKey) {
    rememberView(next);
    setStored(next);
    update({ view: next });
  }

  const items = useMemo(
    () => visibleItems(data.items, readParams(search), clock),
    [data.items, search, clock],
  );
  const counts = useMemo(() => filterCounts(data.items, clock), [data.items, clock]);
  const cards = showAll ? items : items.slice(0, FIRST_CARDS);
  const narrowed = params.filter !== 'all' || params.q.trim() !== '';

  const rows: HeatRow[] = useMemo(
    () =>
      items.map((item) => ({
        id: item.service.id,
        name: item.service.name,
        path: item.service.gitlab_project_path,
        counts: item.last_scan_at ? item.counts : null,
        gate: gateOf(item),
        scannedAt: item.last_scan_at,
        href: `/services/${item.service.id}`,
      })),
    [items],
  );

  return (
    <>
      <PageHeader
        title="Portfolio"
        subtitle="Every service ranked by risk, with its quality gate and how its last pipelines moved."
        freshness={{ at: data.generated_at, now }}
      />
      {notice ? <div className="mb-6">{notice}</div> : null}

      {data.items.length === 0 ? (
        <SetupSteps />
      ) : (
        <>
          <SummaryBand
            items={data.items}
            history={history.data}
            index={index}
            counts={counts}
            filter={params.filter}
            onFilter={(filter) => update({ filter })}
          />

          <div className="mb-4 flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <SearchInput
                primary
                label="Search services"
                placeholder="Search by name or path"
                value={params.q}
                onChange={(q) => update({ q })}
                className="w-full sm:w-72"
              />
              <Select
                label="Sort by"
                hideLabel
                value={params.sort}
                onChange={(sort) => update({ sort: sort as SortKey })}
                options={SORTS.map((s) => ({
                  value: s.value,
                  label: `Sort by ${s.label.toLowerCase()}`,
                }))}
              />
              <Segmented<ViewKey>
                label="View"
                value={view}
                onChange={chooseView}
                options={[
                  {
                    value: 'cards',
                    label: (
                      <span className="inline-flex items-center gap-1.5">
                        <LayoutGrid size={15} strokeWidth={1.75} aria-hidden="true" />
                        Cards
                      </span>
                    ),
                  },
                  {
                    value: 'list',
                    label: (
                      <span className="inline-flex items-center gap-1.5">
                        <Rows3 size={15} strokeWidth={1.75} aria-hidden="true" />
                        List
                      </span>
                    ),
                  },
                ]}
                className="sm:ml-auto"
              />
            </div>
            <FilterChips
              label="Filter services"
              single
              selected={[params.filter]}
              onChange={(selected) => update({ filter: (selected[0] ?? 'all') as FilterKey })}
              options={FILTERS.map((f) => ({ ...f, count: counts[f.value] }))}
            />
          </div>

          <p role="status" className="mb-4 text-body-s text-text-muted tabular-nums">
            {narrowed ? (
              <>
                Showing <span className="font-medium text-text">{formatInt(items.length)}</span> of{' '}
                {formatInt(data.items.length)} {plural(data.items.length, 'service')}
              </>
            ) : (
              <>
                Showing {formatInt(items.length)} of {formatInt(data.items.length)}{' '}
                {plural(data.items.length, 'service')}
              </>
            )}
            {params.filter === 'stale' ? `, no scan in ${STALE_AFTER_DAYS} days` : ''}
          </p>

          {items.length === 0 ? (
            <div className="panel">
              <EmptyState
                compact
                art="search"
                title="No services match"
                body="Change the search or the filter to see more services."
                action={
                  <button
                    type="button"
                    className={secondaryButtonClass}
                    onClick={() => setSearch(new URLSearchParams(), { replace: true })}
                  >
                    Clear search and filter
                  </button>
                }
              />
            </div>
          ) : view === 'list' ? (
            <div className="panel overflow-hidden p-2 sm:p-3">
              <HeatStrip
                rows={rows}
                order={params.sort === 'risk' ? 'risk' : 'given'}
                now={clock}
                staleAfterDays={STALE_AFTER_DAYS}
                caption={
                  params.sort === 'risk'
                    ? 'Services ranked by risk'
                    : `Services sorted by ${params.sort}`
                }
                onSelect={(row) => void navigate(`/services/${row.id}`)}
                renderName={(row) => (
                  <Link to={`/services/${row.id}`} tabIndex={-1} className="hover:underline">
                    {row.name}
                  </Link>
                )}
              />
            </div>
          ) : (
            <>
              <ul
                aria-label="Services"
                className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3"
              >
                <AnimatePresence initial={false} mode="popLayout">
                  {cards.map((item) => (
                    <ServiceCard
                      key={item.service.id}
                      item={item}
                      history={historyFor(item, index)}
                      historyLoading={history.isPending}
                      now={clock}
                      animate={first}
                    />
                  ))}
                </AnimatePresence>
              </ul>
              {cards.length < items.length ? (
                <div className="mt-6 flex flex-wrap items-center justify-center gap-3">
                  <button
                    type="button"
                    className={secondaryButtonClass}
                    onClick={() => setShowAll(true)}
                  >
                    Show all {formatInt(items.length)} services
                  </button>
                  <button
                    type="button"
                    className={secondaryButtonClass}
                    onClick={() => chooseView('list')}
                  >
                    Switch to the list
                  </button>
                </div>
              ) : null}
            </>
          )}
        </>
      )}
    </>
  );
}
