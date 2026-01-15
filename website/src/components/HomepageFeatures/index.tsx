import React, { useState, type ReactNode } from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './styles.module.css';
import indexStyles from '../../pages/index.module.css';

type FeatureItem = {
  title: string;
  Svg: React.ComponentType<React.ComponentProps<'svg'>>;
  description: ReactNode;
};

// Interactive Light Switch Icon
const LightSwitchIcon = (props: React.ComponentProps<'svg'>) => {
  const [isOn, setIsOn] = useState(false);

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="none"
      stroke={isOn ? "#3ecc5f" : "currentColor"}
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      onMouseEnter={() => setIsOn(true)}
      onMouseLeave={() => setIsOn(false)}
      style={{ cursor: 'pointer', transition: 'stroke 0.3s ease' }}
      {...props}
    >
      <rect width="20" height="12" x="2" y="6" rx="6" ry="6" />
      <circle
        cx={isOn ? "16" : "8"}
        cy="12"
        r="2"
        style={{ transition: 'cx 0.3s cubic-bezier(0.18, 0.89, 0.32, 1.28)' }}
      />
    </svg>
  );
};

// Interactive Search to Box Icon
const SearchToBoxIcon = (props: React.ComponentProps<'svg'>) => {
  const [isHovered, setIsHovered] = useState(false);

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill="none"
      stroke={isHovered ? "#3ecc5f" : "currentColor"}
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
      style={{ cursor: 'pointer', transition: 'stroke 0.3s ease' }}
      {...props}
    >
      {/* Magnifying Glass Group */}
      <g
        style={{
          opacity: isHovered ? 0 : 1,
          transform: isHovered ? 'translate(3px, -3px) scale(0.8)' : 'translate(0, 0) scale(1)',
          transformOrigin: 'center',
          transition: 'all 0.3s ease-in-out'
        }}
      >
        <circle cx="11" cy="11" r="8" />
        <path d="m21 21-4.3-4.3" />
      </g>

      {/* Box Group */}
      <g
        style={{
          opacity: isHovered ? 1 : 0,
          transform: isHovered ? 'scale(1)' : 'scale(0.5)',
          transformOrigin: 'center',
          transition: 'all 0.3s cubic-bezier(0.18, 0.89, 0.32, 1.28)'
        }}
      >
        <path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
        <path d="m3.3 7 8.7 5 8.7-5" />
        <path d="M12 22V12" />
      </g>
    </svg>
  );
};

// Interactive Heart Icon
const HeartIcon = (props: React.ComponentProps<'svg'>) => {
  const [isHovered, setIsHovered] = useState(false);

  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      viewBox="0 0 24 24"
      fill={isHovered ? "#ff4d4d" : "none"}
      stroke={isHovered ? "#ff4d4d" : "currentColor"}
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
      style={{
        cursor: 'pointer',
        transition: 'fill 0.3s ease, stroke 0.3s ease',
        animation: isHovered ? `${indexStyles.heartbeat} 1.2s infinite ease-in-out` : 'none',
        transformOrigin: 'center'
      }}
      {...props}
    >
      <path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z" />
    </svg>
  );
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Easy to Setup',
    Svg: LightSwitchIcon,
    description: (
      <>
        Get up and running in minutes
        using Docker or build from source into a single, portable binary.
      </>
    ),
  },
  {
    title: 'Physical Search',
    Svg: SearchToBoxIcon,
    description: (
      <>
        Stop digging through bins. Instantly find your inventory via native WLED integration. Open source <a href="https://github.com/tuxedocurly/wledger/tree/main/hardware" target="_blank">hardware</a> and <Link to="/docs/Hardware/build-guide">build guide</Link> included.
      </>
    ),
  },
  {
    title: 'FOSS Forever',
    Svg: HeartIcon,
    description: (
      <>
        WLEDger is free and open-source. No subscriptions, telemetry, or analytics. Consider supporting development by <a href="https://ko-fi.com/tuxedomakes" target="_blank">buying me a coffee</a>!
      </>
    ),
  },
];

function Feature({ title, Svg, description }: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className={styles.featureCard}>
        <div className="text--center">
          <Svg className={styles.featureSvg} role="img" />
        </div>
        <div className="text--center padding-horiz--md">
          <Heading as="h3">{title}</Heading>
          <p>{description}</p>
        </div>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}