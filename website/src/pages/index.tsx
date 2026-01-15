import React, { useRef, useEffect, useState, type ReactNode } from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function InteractiveGrid() {
  const containerRef = useRef<HTMLDivElement>(null);
  const [mousePos, setMousePos] = useState({ x: -1000, y: -1000 });
  const [gridSize, setGridSize] = useState({ rows: 0, cols: 0 });

  useEffect(() => {
    const handleResize = () => {
      if (containerRef.current) {
        const { width, height } = containerRef.current.getBoundingClientRect();
        // Calculate rows and cols based on a fixed spacing
        const spacing = 30; // px
        const cols = Math.floor(width / spacing);
        const rows = Math.floor(height / spacing);
        setGridSize({ rows, cols });
      }
    };

    handleResize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const handleMouseMove = (e: React.MouseEvent) => {
    if (containerRef.current) {
      const rect = containerRef.current.getBoundingClientRect();
      setMousePos({
        x: e.clientX - rect.left,
        y: e.clientY - rect.top,
      });
    }
  };

  const handleMouseLeave = () => {
    setMousePos({ x: -1000, y: -1000 });
  };

  return (
    <div
      className={styles.gridContainer}
      ref={containerRef}
      onMouseMove={handleMouseMove}
      onMouseLeave={handleMouseLeave}
    >
      <div className={styles.gridOverlay} />
      {Array.from({ length: gridSize.rows * gridSize.cols }).map((_, i) => {
        const row = Math.floor(i / gridSize.cols);
        const col = i % gridSize.cols;
        // Approximate position of the dot center
        const dotX = col * 30 + 15;
        const dotY = row * 30 + 15;

        // Calculate distance
        const dx = mousePos.x - dotX;
        const dy = mousePos.y - dotY;
        const dist = Math.sqrt(dx * dx + dy * dy);

        // Calculate brightness/scale based on distance (closer = brighter/larger)
        const maxDist = 200;
        const intensity = Math.max(0, 1 - dist / maxDist);

        return (
          <div
            key={i}
            className={styles.ledDot}
            style={{
              left: col * 30 + 'px',
              top: row * 30 + 'px',
              opacity: 0.1 + (intensity * 0.9),
              transform: `scale(${1 + intensity * 0.5})`,
              boxShadow: intensity > 0.1 ? `0 0 ${intensity * 10}px var(--ifm-color-primary)` : 'none'
            }}
          />
        );
      })}
    </div>
  );
}

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <header className={clsx('hero', styles.heroBanner)}>
      <InteractiveGrid />
      <div className={clsx("container", styles.heroContent)}>
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="/docs/Software/quickstart-guide">
            Get WLEDger
          </Link>
        </div>
      </div>
    </header>
  );
}

export default function Home(): ReactNode {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout
      title={`Welcome to ${siteConfig.title}`}
      description="WLEDger: CTRL + F for physical objects. Manage your inventory. Find your things visually using WLED.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
      </main>
    </Layout>
  );
}
