// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router';
import type { Portfolio } from '../../api/portfolio';
import { Chart } from '../../charts/Chart';
import { treemap, type TreemapItem } from '../../charts/options';
import { FilterChips } from '../../components/FilterChips';
import { Select, SearchInput } from '../../components/inputs';
import { Card, CardHeader, Grid, PageHeader } from '../../components/layout';
import { Segmented } from '../../components/Segmented';
import { StatTile } from '../../components/StatTile';
import { worstSeverity } from '../../components/severityModel';
import { EmptyState } from '../../components/states';
import { TargetLink } from '../../components/Freshness';
import { secondaryButtonClass } from '../../ui';
import { plural } from '../../format';
import {
  FILTERS,
  SORTS,
  filterCounts,
  openVulnerabilities,
  readParams,
  visibleItems,
  writeParams,
  type FilterKey,
  type PortfolioParams,
  type SortKey,
  type ViewKey,
} from './model';
import { CI_GUIDE_URL, ServiceTile } from './ServiceTile';

const SONARQUBE_GUIDE_URL =
  'https://github.com/siyamsarker/scanopsis/blob/main/docs/integrations/sonarqube.md';

const linkClass = 'text-text underline underline-offset-2';

export function ConnectEmpty() {
  return (
    <Card padded={false}>
      <EmptyState
        variant="connect"
        title="No services yet"
        body="Services appear here after the first pipeline uploads a scan."
        steps={[
          {
            title: 'Add the GitLab URL',
            body: (
              <>
                Set it in{' '}
                <Link to="/settings" className={linkClass}>
                  GitLab settings
                </Link>{' '}
                so pipelines can be verified.
              </>
            ),
          },
          {
            title: 'Add the upload step to the pipeline',
            body: (
              <TargetLink
                target={{ label: 'Read the GitLab CI guide', href: CI_GUIDE_URL }}
                className={linkClass}
              />
            ),
          },
          {
            title: 'Connect SonarQube',
            body: (
              <TargetLink
                target={{ label: 'Read the SonarQube guide', href: SONARQUBE_GUIDE_URL }}
                className={linkClass}
              />
            ),
          },
        ]}
      />
    </Card>
  );
}

/** Summary, controls and the grid or treemap for a loaded portfolio. `now` fixes the clock for tests. */
export function PortfolioView({ data, now }: { data: Portfolio; now?: Date }) {
  const [search, setSearch] = useSearchParams();
  const navigate = useNavigate();
  const [mounted] = useState(() => new Date());
  const clock = now ?? mounted;
  const params = readParams(search);
  const { totals } = data;

  function update(patch: Partial<PortfolioParams>) {
    setSearch(writeParams({ ...params, ...patch }), { replace: true });
  }

  const items = useMemo(
    () => visibleItems(data.items, readParams(search), clock),
    [data.items, search, clock],
  );
  const counts = useMemo(() => filterCounts(data.items, clock), [data.items, clock]);
  const treemapOption = useMemo(() => {
    const tiles: TreemapItem[] = items.map((item) => {
      const open = openVulnerabilities(item);
      return {
        id: String(item.service.id),
        name: item.service.name,
        // Zero is drawn as a sliver: echarts drops tiles of size 0, and formatInt shows 0.
        value: open === 0 ? 0.2 : open,
        severity: worstSeverity(item.counts),
        detail: item.service.gitlab_project_path,
      };
    });
    return treemap(tiles, { valueLabel: 'open vulnerabilities' });
  }, [items]);

  return (
    <>
      <PageHeader
        title="Portfolio"
        subtitle="Every service with its open vulnerabilities, quality gate and freshness."
        freshness={{ at: data.generated_at, now }}
      />

      <section aria-label="Summary" className="mb-8">
        <Grid cols={4}>
          <StatTile label="Services" value={totals.services} to="/" />
          <StatTile
            label="Failing quality gates"
            value={totals.failing_quality_gates}
            to="/?filter=gate-failed"
            tone={totals.failing_quality_gates > 0 ? 'fail' : undefined}
          />
          <StatTile
            label="Services with critical vulnerabilities"
            value={totals.services_with_critical}
            to="/?filter=critical"
            tone={totals.services_with_critical > 0 ? 'critical' : undefined}
          />
          <StatTile
            label="Fixable vulnerabilities"
            value={totals.fixable}
            to="/?filter=fixable"
            hint="A fixed version exists"
          />
        </Grid>
      </section>

      {data.items.length === 0 ? (
        <ConnectEmpty />
      ) : (
        <>
          <div className="mb-6 flex flex-col gap-4">
            <div className="flex flex-wrap items-end gap-3">
              <SearchInput
                label="Search services"
                placeholder="Search by name or path"
                value={params.q}
                onChange={(q) => update({ q })}
                className="w-full sm:w-80"
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
                value={params.view}
                onChange={(view) => update({ view })}
                options={[
                  { value: 'grid', label: 'Grid' },
                  { value: 'treemap', label: 'Treemap' },
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

          <p role="status" className="mb-4 text-sm text-text-muted">
            Showing {items.length} of {data.items.length} {plural(data.items.length, 'service')}
          </p>

          {items.length === 0 ? (
            <Card padded={false}>
              <EmptyState
                compact
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
            </Card>
          ) : params.view === 'treemap' ? (
            <Card padded={false}>
              <CardHeader
                padded
                title="Which services carry the most open vulnerabilities?"
                description="Tile size is the number of open vulnerabilities; colour is the worst severity. Click a tile to open the service."
              />
              <Chart
                option={treemapOption}
                ariaLabel="Open vulnerabilities per service, sized by count and coloured by worst severity"
                heightClass="h-[28rem]"
                className="px-4"
                onItemClick={({ data: tile }) => {
                  const id = (tile as { id?: string } | undefined)?.id;
                  if (id) void navigate(`/services/${id}`);
                }}
              />
              <p className="px-6 py-4 text-sm text-text-muted">
                Treemap tiles are also listed in the grid view.
              </p>
            </Card>
          ) : (
            <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4">
              {items.map((item) => (
                <ServiceTile key={item.service.id} item={item} now={now} />
              ))}
            </ul>
          )}
        </>
      )}
    </>
  );
}
