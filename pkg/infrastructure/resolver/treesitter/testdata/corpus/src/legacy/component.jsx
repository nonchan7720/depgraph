import { Button } from '../ui/Button';

export const C = () => <div className="c">{[1, 2].map((x) => <Button key={x} />)}</div>;
export function D() {
  return <C />;
}
