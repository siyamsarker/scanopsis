// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

import type { Vulnerability } from '../../api/findings';
import { targetLabel } from './FindingsList';
import {
  compareVersions,
  groupRows,
  highestFixed,
  parseGroupBy,
  suggestedFix,
  upgradeCommand,
  upgradeGroups,
} from './model';

function v(id: number, over: Partial<Vulnerability> = {}): Vulnerability {
  return {
    id,
    scan_id: 1,
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

describe('compareVersions', () => {
  it('compares numbers numerically', () => {
    expect(compareVersions('1.2.10', '1.2.9')).toBeGreaterThan(0);
    expect(compareVersions('1.2.9', '1.2.10')).toBeLessThan(0);
    expect(compareVersions('2.0', '2.0.0')).toBeLessThan(0);
    expect(compareVersions('3.0.1', '3.0.1')).toBe(0);
  });

  it('compares text lexically and ranks pre-releases lower', () => {
    expect(compareVersions('1.0.0-beta', '1.0.0-alpha')).toBeGreaterThan(0);
    expect(compareVersions('1.2.3', '1.2.3-rc1')).toBeGreaterThan(0);
    expect(compareVersions('1.2.3-rc1', '1.2.3')).toBeLessThan(0);
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
    v(3, { pkg_name: 'zlib', severity: 'LOW', artifact_name: 'worker' }),
    v(4, { pkg_name: 'zlib', severity: 'LOW', vuln_id: 'CVE-1', artifact_name: 'worker' }),
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
});

describe('targetLabel', () => {
  it('drops the image name Trivy repeats in an OS target', () => {
    expect(targetLabel('python:3.9 (debian 10.13)', 'python:3.9')).toBe('debian 10.13');
    expect(targetLabel('app/package-lock.json', 'app:1')).toBe('app/package-lock.json');
    expect(targetLabel('python:3.9', 'python:3.9')).toBe('python:3.9');
  });
});
