// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Vulnerability } from '../../api/findings';
import upgradeRules from '../../../../internal/domain/testdata/upgrade-rules.json';
import { targetLabel } from './FindingsList';
import {
  applyFilters,
  artifactIdOf,
  compareVersions,
  groupRows,
  highestFixed,
  parseFilters,
  parseGroupBy,
  resolveFilters,
  suggestedFix,
  upgradeCommand,
  upgradeGroups,
  withScanKinds,
} from './model';

function v(id: number, over: Partial<Vulnerability> = {}): Vulnerability {
  return {
    id,
    scan_id: 1,
    artifact_key: 'app',
    artifact_name: 'app',
    target: 'debian 12',
    class: 'os-pkgs',
    pkg_type: 'debian',
    pkg_name: 'pkg',
    pkg_path: '',
    installed_version: '1.0',
    fixed_version: '',
    vuln_id: `CVE-${id}`,
    severity: 'HIGH',
    status: '',
    title: `Title ${id}`,
    primary_url: '',
    suppressed: false,
    suppression: '',
    first_seen_at: '2026-11-01T00:00:00Z',
    ...over,
  };
}

// The cases the server's tests use too (internal/domain), so Fix first on
// the service page and the upgrade plan here follow the same rules.
interface UpgradeRules {
  compare: [string, string, number][];
  highest: [string, string][];
  findings: {
    pkg: string;
    installed: string;
    severity: Vulnerability['severity'];
    fixed: string;
    suppressed?: boolean;
  }[];
  plan: {
    pkg: string;
    installed: string;
    upgrade_to: string;
    cleared: number;
    risk: number;
    counts: Record<string, number>;
  }[];
}
const rules = upgradeRules as unknown as UpgradeRules;

describe('compareVersions', () => {
  it.each(rules.compare)('compares %j with %j as the server does', (a, b, want) => {
    expect(Math.sign(compareVersions(a, b))).toBe(want);
    expect(Math.sign(compareVersions(b, a))).toBe(-want || 0);
  });

  it.each(rules.highest)('finds the highest of %j as the server does', (list, want) => {
    expect(highestFixed([list])).toBe(want);
  });
});

describe('the upgrade plan and Fix first', () => {
  it('pick the same target, counts and order', () => {
    const plan = upgradeGroups(
      rules.findings.map((f, i) =>
        v(i + 1, {
          pkg_name: f.pkg,
          installed_version: f.installed,
          severity: f.severity,
          fixed_version: f.fixed,
          suppressed: f.suppressed ?? false,
          suppression: f.suppressed ? 'accepted' : '',
        }),
      ),
    );
    expect(
      plan.map((g) => ({
        pkg: g.pkg,
        installed: g.installed,
        upgrade_to: g.upgradeTo,
        cleared: g.cleared,
        risk: g.risk,
        counts: g.counts,
      })),
    ).toEqual(rules.plan);
  });
});

describe('highestFixed', () => {
  it('splits comma-separated lists and picks the highest', () => {
    expect(highestFixed(['3.0.9', '3.0.10, 3.1.2'])).toBe('3.1.2');
    expect(highestFixed(['1.2.9', '1.2.10'])).toBe('1.2.10');
    expect(highestFixed([])).toBe('');
  });
});

describe('suggestedFix', () => {
  it('picks the lowest listed version above the installed one', () => {
    expect(suggestedFix('1.0.0', '1.1.2, 1.0.5')).toBe('1.0.5');
    expect(suggestedFix('1.0.7', '1.0.5, 1.1.2')).toBe('1.1.2');
    expect(suggestedFix('9.0', '1.0.5, 1.1.2')).toBe('1.1.2');
    expect(suggestedFix('1.0', '')).toBe('');
  });
});

describe('upgradeCommand', () => {
  it('uses the tool of the ecosystem and quotes what needs it', () => {
    expect(upgradeCommand('debian', 'openssl', '3.0.9')).toBe(
      'apt-get install --only-upgrade openssl=3.0.9',
    );
    expect(upgradeCommand('alpine', 'zlib', '1.3-r0')).toBe('apk add --upgrade zlib=1.3-r0');
    expect(upgradeCommand('redhat', 'curl', '8.1')).toBe('dnf upgrade curl-8.1');
    expect(upgradeCommand('npm', '@scope/pkg', '2.0.1')).toBe('npm install @scope/pkg@2.0.1');
    expect(upgradeCommand('pip', 'Jinja2', '3.1.4')).toBe('pip install Jinja2==3.1.4');
    expect(upgradeCommand('gomod', 'golang.org/x/net', '0.23.0')).toBe(
      'go get golang.org/x/net@v0.23.0',
    );
    expect(upgradeCommand('pom', 'org.yaml:snakeyaml', '2.0')).toBe('org.yaml:snakeyaml:2.0');
  });

  it('has none when the name or version could mean something to a shell', () => {
    const unsafe = [
      'evil;pkg',
      "x'; rm -rf ~; '",
      '$(curl example.com)',
      '`id`',
      'a\nrm -rf ~',
      'a"b',
      'a b',
      'a|b',
      'a&b',
      'a>b',
      '-rf',
      '~root',
      '',
    ];
    for (const value of unsafe) {
      expect(upgradeCommand('debian', value, '1.0')).toBeNull();
      expect(upgradeCommand('npm', 'left-pad', value)).toBeNull();
    }
  });

  it('has none for unknown ecosystems, the Go standard library or no version', () => {
    expect(upgradeCommand('conan', 'zlib', '1.3')).toBeNull();
    expect(upgradeCommand('gobinary', 'stdlib', '1.22.3')).toBeNull();
    expect(upgradeCommand('npm', 'left-pad', '')).toBeNull();
  });
});

describe('grouping', () => {
  const rows = [
    v(1, { pkg_name: 'openssl', severity: 'CRITICAL', fixed_version: '3.0.9' }),
    v(2, { pkg_name: 'openssl', fixed_version: '3.0.10, 3.1.2' }),
    v(3, { pkg_name: 'zlib', severity: 'LOW', artifact_key: 'worker', artifact_name: 'worker' }),
    v(4, {
      pkg_name: 'zlib',
      severity: 'LOW',
      vuln_id: 'CVE-1',
      artifact_key: 'worker',
      artifact_name: 'worker',
    }),
  ];

  it('groups by package and ranks groups by risk points', () => {
    const groups = groupRows(rows, 'package');
    expect(groups.map((g) => [g.label, g.risk, g.rows.length, g.fixable, g.upgradeTo])).toEqual([
      ['openssl', 15, 2, 2, '3.1.2'],
      ['zlib', 2, 2, 0, undefined],
    ]);
  });

  it('groups by vulnerability and by image', () => {
    expect(groupRows(rows, 'vulnerability').map((g) => [g.label, g.rows.length])).toEqual([
      ['CVE-1', 2],
      ['CVE-2', 1],
      ['CVE-3', 1],
    ]);
    expect(groupRows(rows, 'image').map((g) => [g.label, g.risk])).toEqual([
      ['app', 15],
      ['worker', 2],
    ]);
  });

  it('reads the grouping from the URL', () => {
    expect(parseGroupBy('package')).toBe('package');
    expect(parseGroupBy('nonsense')).toBe('none');
    expect(parseGroupBy(null)).toBe('none');
  });

  it('ranks upgrades by risk cleared and leaves suppressed findings out', () => {
    const plan = upgradeGroups([
      ...rows,
      v(5, { pkg_name: 'curl', severity: 'CRITICAL', fixed_version: '8.1', suppressed: true }),
      v(6, { pkg_name: 'bash', severity: 'MEDIUM', fixed_version: '5.2' }),
    ]);
    expect(plan.map((g) => [g.pkg, g.risk, g.cleared, g.vulnIds])).toEqual([
      ['openssl', 15, 2, ['CVE-1', 'CVE-2']],
      ['bash', 3, 1, ['CVE-6']],
    ]);
  });

  it('counts what the upgrade to the highest fixed version fixes, as Fix first does', () => {
    // libc-bin: 3 high, 3 medium and 2 low open, with different fixed
    // versions; the server's fixable_by_severity is 2 high, 1 medium, 2 low.
    const libc = (id: number, severity: Vulnerability['severity'], fixed: string) =>
      v(id, {
        pkg_name: 'libc-bin',
        installed_version: '2.36-9+deb12u3',
        severity,
        fixed_version: fixed,
      });
    const [plan] = upgradeGroups([
      libc(1, 'HIGH', '2.36-9+deb12u7'),
      libc(2, 'HIGH', '2.36-9+deb12u10'),
      libc(3, 'HIGH', ''),
      libc(4, 'MEDIUM', '2.36-9+deb12u4'),
      libc(5, 'MEDIUM', ''),
      libc(6, 'MEDIUM', ''),
      libc(7, 'LOW', '2.36-9+deb12u4'),
      libc(8, 'LOW', '2.36-9+deb12u9'),
    ]);
    expect(plan.upgradeTo).toBe('2.36-9+deb12u10');
    expect(plan.counts).toEqual({ critical: 0, high: 2, medium: 1, low: 2, unknown: 0 });
    expect([plan.cleared, plan.risk]).toEqual([5, 15]);
  });
});

describe('targetLabel', () => {
  it('drops the image name Trivy repeats in an OS target', () => {
    expect(targetLabel('python:3.9 (debian 10.13)', 'python:3.9')).toBe('debian 10.13');
    expect(targetLabel('app/package-lock.json', 'app:1')).toBe('app/package-lock.json');
    expect(targetLabel('python:3.9', 'python:3.9')).toBe('python:3.9');
  });

  it('drops the artifact key an OS target is stored under, as a whole word only', () => {
    expect(targetLabel('web-frontend-stage (alpine 3.20.3)', 'web-frontend-stage')).toBe(
      'alpine 3.20.3',
    );
    expect(targetLabel('app/package-lock.json', 'app')).toBe('app/package-lock.json');
    expect(targetLabel('app-worker (alpine 3.20.3)', 'app')).toBe('app-worker (alpine 3.20.3)');
  });
});

describe('artifact key', () => {
  const stage = { artifact_key: 'web-frontend-stage', artifact_name: 'web-frontend-stage:4' };
  const rows = [
    v(1, { ...stage, pkg_name: 'openssl', severity: 'CRITICAL', fixed_version: '3.3.2' }),
    v(2, { ...stage, pkg_name: 'musl' }),
    v(3, {
      artifact_key: 'worker',
      artifact_name: 'worker:9',
      pkg_name: 'openssl',
      fixed_version: '3.3.2',
    }),
    v(4, {
      artifact_key: 'registry.example.com:5000/team/app',
      artifact_name: 'registry.example.com:5000/team/app:7',
      pkg_name: 'zlib',
    }),
  ];
  const ids = (list: Vulnerability[]) => list.map((r) => r.id);
  const filter = (query: string, list: Vulnerability[] = rows) =>
    ids(applyFilters(list, resolveFilters(parseFilters(new URLSearchParams(query)), list)));

  it('filters by the key, and by a full name from an older link', () => {
    expect(filter('artifact=web-frontend-stage')).toEqual([1, 2]);
    expect(filter('artifact=web-frontend-stage:4')).toEqual([1, 2]);
    expect(filter('artifact=worker,web-frontend-stage')).toEqual([1, 2, 3]);
    expect(filter('artifact=other')).toEqual([]);
  });

  it('filters an older tag or digest of an image by the image', () => {
    expect(filter('artifact=web-frontend-stage:3')).toEqual([1, 2]);
    expect(filter('artifact=worker@sha256:0d2b1e8f')).toEqual([3]);
    // A registry port is not a tag.
    expect(filter('artifact=registry.example.com:5000/team/app:6')).toEqual([4]);
    expect(filter('artifact=registry.example.com:5000/team/app')).toEqual([4]);
    expect(filter('artifact=registry.example.com:5000/team')).toEqual([]);
  });

  it('turns every name of the filter into its artifact', () => {
    const f = resolveFilters(parseFilters(new URLSearchParams('artifact=worker:1,worker')), rows);
    expect(f.artifacts).toEqual(['worker']);
  });

  it('groups by image under the key, with the latest name as detail', () => {
    expect(groupRows(rows, 'image').map((g) => [g.label, g.latest, g.rows.length])).toEqual([
      ['web-frontend-stage', 'web-frontend-stage:4', 2],
      ['registry.example.com:5000/team/app', 'registry.example.com:5000/team/app:7', 1],
      ['worker', 'worker:9', 1],
    ]);
  });

  it('names the images an upgrade clears by key', () => {
    expect(upgradeGroups(rows).map((g) => [g.pkg, g.artifacts])).toEqual([
      ['openssl', ['web-frontend-stage', 'worker']],
    ]);
  });

  describe('a secret scan with the key of an image', () => {
    // Scan 9 is a secret scan of the folder "worker" that also reported packages.
    const secret = v(5, {
      scan_id: 9,
      artifact_key: 'worker',
      artifact_name: '/builds/x/0/demo/web-frontend/worker',
      pkg_name: 'openssl',
      fixed_version: '3.3.2',
    });
    const mixed = withScanKinds(
      [...rows, secret],
      [
        { id: 1, kind: 'image' },
        { id: 9, kind: 'secret-fs' },
      ],
    );

    it('is an artifact of its own', () => {
      expect(artifactIdOf(mixed[2])).toBe('worker');
      expect(artifactIdOf(mixed[4])).toBe('secret-fs:worker');
      expect(filter('artifact=worker', mixed)).toEqual([3]);
      expect(filter('artifact=secret-fs:worker', mixed)).toEqual([5]);
      expect(groupRows(mixed, 'image').map((g) => [g.label, g.rows.map((r) => r.id)])).toEqual([
        ['web-frontend-stage', [1, 2]],
        ['registry.example.com:5000/team/app', [4]],
        ['worker', [3]],
        ['worker (secret scan)', [5]],
      ]);
      expect(upgradeGroups(mixed)[0].artifacts).toEqual([
        'web-frontend-stage',
        'worker',
        'worker (secret scan)',
      ]);
    });

    it('leaves the rows as they are when every scan is an image', () => {
      expect(withScanKinds(rows, [{ id: 1, kind: 'image' }])).toBe(rows);
      expect(withScanKinds(rows, undefined)).toBe(rows);
    });
  });
});
