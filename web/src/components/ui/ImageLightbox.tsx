import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { useState } from 'react';

type ImageLightboxProps = {
  src: string;
  alt: string;
  className?: string;
  loading?: 'eager' | 'lazy';
};

export function ImageLightbox({ src, alt, className, loading }: ImageLightboxProps) {
  const [open, setOpen] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <button
        type="button"
        className="image-lightbox-trigger"
        aria-label={`View ${alt} full size`}
        onClick={() => setOpen(true)}
      >
        <img className={className} src={src} alt={alt} loading={loading} />
      </button>
      <DialogContent className="image-lightbox">
        <DialogTitle className="sr-only">{alt} full size</DialogTitle>
        <img src={src} alt={alt} />
      </DialogContent>
    </Dialog>
  );
}
