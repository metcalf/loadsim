import numpy as np
import matplotlib.pyplot as plt
from matplotlib import cm, colors
import sys
from collections import defaultdict

filenames = sys.argv[1:]
groups = defaultdict(list)
for filename in filenames:
    ts, worker, run = filename.split(".")[0].split("_")
    groups[(ts, run)].append((worker, filename))

for (ts, run), group in groups.iteritems():
    f, axes = plt.subplots(len(group), sharex=True, sharey=True)
    plots = []

    ymax = 1
    xmax = 1
    for ax, (worker, filename) in zip(axes, group):
        ax.set_title("%s %s" % (worker, run))

        data = np.genfromtxt(filename, dtype=None, delimiter='\t', names=True)
        ymax = max((ymax, max(data['total_time'])+1))
        xmax = max((xmax, max(data['end'])))

        agents = np.unique(data['agent'])

        scalarMap = cm.ScalarMappable(
            norm=colors.Normalize(vmin=0, vmax=len(agents)-1),
            cmap=plt.get_cmap('jet')
        )

        for i, agent in enumerate(agents):
            series = data[np.where(data['agent'] == agent)]

            plot = ax.scatter(
                series['request_start'], series['total_time'],
                c=([scalarMap.to_rgba(i)] * len(series)),
                edgecolors='none',
                s=5,
            )

            plots.append(plot)

    for ax in f.axes:
        ax.set_ylim(0, ymax+1)
        ax.set_xlim(-1, xmax)

    # Fine-tune figure; make subplots close to each other and hide x ticks for
    # all but bottom plot.
    plt.setp([a.get_xticklabels() for a in f.axes[:-1]], visible=False)
    # f.subplots_adjust(hspace=0)

    f.legend(
        plots, agents,
        loc='upper left',
        prop={'size':10},
    )

    f.savefig("%s_%s.png" % (ts, run), bbox_inches='tight', dpi=300)
    # f.set_xlim(-1, 90)
