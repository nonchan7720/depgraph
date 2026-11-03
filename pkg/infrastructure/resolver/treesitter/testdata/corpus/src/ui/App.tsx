import React, { useState } from "react";
import { Button } from "./Button";
import styles from "./App.module.css";

export interface Props {
  title: string;
}

export const App = ({ title }: Props) => {
  const [n, setN] = useState(0);
  return (
    <div className={styles.root}>
      <Button onClick={() => setN(n + 1)}>
        {title} {n}
      </Button>
    </div>
  );
};

export default class Page extends React.Component<Props> {
  render() {
    return <App title="x" />;
  }
}

function Inner() {
  return (
    <>
      <span>a &amp; b</span>
    </>
  );
}

const generic = <T,>(x: T) => x;
