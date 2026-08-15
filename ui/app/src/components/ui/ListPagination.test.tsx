import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ListPagination } from './ListPagination';

describe('ListPagination', () => {
  it('renders range and disables First/Previous on page 1', () => {
    const onPageChange = vi.fn();
    render(<ListPagination page={1} pageSize={25} total={100} onPageChange={onPageChange} />);

    // Range summary: "Showing 1–25 of 100"
    expect(screen.getByText(/Showing/i).textContent).toMatch(/1/);
    expect(screen.getByText(/Showing/i).textContent).toMatch(/25/);
    expect(screen.getByText(/Showing/i).textContent).toMatch(/100/);
    expect(screen.getByRole('button', { name: 'Page 1' })).toHaveAttribute('aria-current', 'page');

    expect(screen.getByRole('button', { name: 'First page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Next page' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: 'Last page' })).not.toBeDisabled();
  });

  it('navigates First / Prev / numbered / Next / Last', () => {
    const onPageChange = vi.fn();
    render(<ListPagination page={3} pageSize={25} total={100} onPageChange={onPageChange} />);

    fireEvent.click(screen.getByRole('button', { name: 'First page' }));
    expect(onPageChange).toHaveBeenCalledWith(1);

    fireEvent.click(screen.getByRole('button', { name: 'Previous page' }));
    expect(onPageChange).toHaveBeenCalledWith(2);

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }));
    expect(onPageChange).toHaveBeenCalledWith(4);

    fireEvent.click(screen.getByRole('button', { name: 'Last page' }));
    expect(onPageChange).toHaveBeenCalledWith(4); // 100/25 = 4 pages

    fireEvent.click(screen.getByRole('button', { name: 'Page 2' }));
    expect(onPageChange).toHaveBeenCalledWith(2);
  });

  it('disables Next/Last on the last page', () => {
    render(<ListPagination page={4} pageSize={25} total={100} onPageChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Last page' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'First page' })).not.toBeDisabled();
  });

  it('returns null when total is 0', () => {
    const { container } = render(
      <ListPagination page={1} pageSize={25} total={0} onPageChange={vi.fn()} />
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('marks the current page with aria-current', () => {
    render(<ListPagination page={2} pageSize={10} total={50} onPageChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Page 2' })).toHaveAttribute('aria-current', 'page');
  });
});
