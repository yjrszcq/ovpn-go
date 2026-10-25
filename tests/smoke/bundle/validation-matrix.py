"""Generate all native bundle acceptance jobs from the pinned distribution inventory."""
import json
from pathlib import Path
import re
import sys


def validation_matrix(inventory):
    distributions = [entry['id'] for entry in inventory['distributions']]
    if (not distributions or len(set(distributions)) != len(distributions)
            or any(not re.fullmatch(r'[a-z][a-z0-9-]*', item) or item in ('arch', 'scratch')
                   for item in distributions)):
        raise ValueError('distribution IDs must be unique and may not use arch/scratch')
    for entry in [*inventory['distributions'], inventory['arch_amd64']]:
        if not re.search(r'@sha256:[0-9a-f]{64}$', entry['image']):
            raise ValueError('distribution images must be pinned by digest')
    rootfs = inventory['arch_arm64']
    if not re.fullmatch(r'[0-9a-f]{64}', rootfs['sha256']) or not rootfs['url'].startswith('https://'):
        raise ValueError('Arch Linux ARM rootfs requires HTTPS and a pinned SHA-256')
    jobs = []
    for arch, runner, machine in [('amd64', 'ubuntu-latest', 'x86_64'),
                                   ('arm64', 'ubuntu-24.04-arm', 'aarch64')]:
        for distribution in [*distributions, 'arch', 'scratch']:
            jobs.append(dict(arch=arch, runner=runner, machine=machine, distribution=distribution))
    return {'include': jobs}


if __name__ == '__main__':
    path = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[3] / 'packaging/bundle/test-matrix.json'
    print(json.dumps(validation_matrix(json.loads(path.read_text())), separators=(',', ':')))
