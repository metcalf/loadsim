import numpy as np
import matplotlib.pyplot as plt
from matplotlib import cm, colors
import sys

data = np.genfromtxt(sys.stdin, dtype=None, delimiter='\t', names=True)

agents = np.unique(data['agent'])

scalarMap = cm.ScalarMappable(
    norm=colors.Normalize(vmin=0, vmax=len(agents)-1),
    cmap=plt.get_cmap('jet')
)

plots = []
for i, agent in enumerate(agents):
    series = data[np.where(data['agent'] == agent)]

    plot = plt.scatter(
        series['request_start'], series['total_time'],
        c=([scalarMap.to_rgba(i)] * len(series)),
        edgecolors='none',
    )

    plots.append(plot)

plt.legend(
    plots, agents,
    loc='upper left',
)
plt.xlim(-1, max(data['end']))
plt.ylim(0, 60)
plt.show()
